package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

const (
	// PasskeyProviderID stands for passkeys wherever a sign-in provider ID goes, such as the session and the login page
	PasskeyProviderID = "passkey"
	// passkeyIssuer is the issuer of passkey accounts, which no sign-in provider vouches for
	passkeyIssuer = "passkey"

	passkeyCookieName = "umpteenth_passkey"
	// The ceremony cookie reaches both the sign-in endpoints and those that add a passkey to the signed-in account
	passkeyCookiePath  = "/api"
	passkeyCeremonyTTL = 5 * time.Minute

	// SignInLinkPath is the page a sign-in link opens, followed by the link's token
	SignInLinkPath = "/login/link/"
	signInLinkTTL  = 24 * time.Hour
	// accountPath is where a sign-in link lands, which asks for a passkey when the account has none
	accountPath = "/account"

	defaultPasskeyName = "Passkey"
)

// Kinds of passkey ceremonies, so the browser's answer only finishes the kind of ceremony that asked for it
const (
	ceremonySignIn = "sign-in"
	ceremonySignUp = "sign-up"
	ceremonyAdd    = "add"
)

// passkeyCeremony is what a ceremony remembers between handing out its options and checking the browser's answer
type passkeyCeremony struct {
	Session webauthn.SessionData `json:"session"`
	// UserID is the account that gets the new passkey, and a sign-up creates its account under this ID
	UserID string `json:"userId,omitempty"`
	// Name and Email are what a sign-up creates its account with
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// Redirect is where the browser goes after signing in or up, an invite page for a sign-up through an invite link
	Redirect string `json:"redirect,omitempty"`
}

// passkeyAccount is a passkey account as WebAuthn sees it
type passkeyAccount struct {
	user authdb.User
	// passkeys are the stored rows of credentials, in the same order
	passkeys    []authdb.Passkey
	credentials []webauthn.Credential
}

// The user handle is the account's ID, which the authenticator hands back at sign-in to name the account
func (a passkeyAccount) WebAuthnID() []byte { return []byte(a.user.ID) }

// The authenticator lists the passkey under the email address, or the name of an account without one
func (a passkeyAccount) WebAuthnName() string { return firstOf(a.user.Email, a.user.Name) }

func (a passkeyAccount) WebAuthnDisplayName() string { return firstOf(a.user.Name, a.user.Email) }

func (a passkeyAccount) WebAuthnCredentials() []webauthn.Credential { return a.credentials }

// SignUp is who a new passkey account is for
type SignUp struct {
	Name  string
	Email string
	// Redirect is where the browser goes afterwards, and an invite page there is what lets someone without an account sign up
	Redirect string
}

// SignInLink signs in a passkey account once, so its owner can add a passkey
type SignInLink struct {
	URL       string
	ExpiresAt int64
}

// newRelyingParty sets up WebAuthn for the app's URL, whose host every passkey is bound to
func newRelyingParty(appURL string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(appURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("passkeys need app.url to be an absolute URL, got %q", appURL)
	}
	rp, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Umpteenth",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
		// Passkeys are discoverable credentials that the authenticator unlocks with a PIN or biometrics, which makes them a sign-in on their own
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set up passkeys: %w", err)
	}
	return rp, nil
}

// PasskeysEnabled reports whether people can sign in and sign up with passkeys
func (s *Service) PasskeysEnabled() bool {
	return s.rp != nil
}

// relyingParty returns the WebAuthn setup, or an error when passkeys are turned off
func (s *Service) relyingParty() (*webauthn.WebAuthn, error) {
	if s.rp == nil {
		return nil, apperror.Forbidden("Passkeys are turned off on this instance")
	}
	return s.rp, nil
}

// SetupOpen reports whether the instance has no users yet, so whoever opens it first creates its first account
func (s *Service) SetupOpen(ctx context.Context) (bool, error) {
	if s.rp == nil {
		return false, nil
	}
	n, err := s.queries.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to count users: %w", err)
	}
	return n == 0, nil
}

// BeginPasskeySignIn returns the options of a sign-in with any passkey of the instance and the cookie of its ceremony
func (s *Service) BeginPasskeySignIn(ctx context.Context, redirect string) (json.RawMessage, http.Cookie, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return nil, http.Cookie{}, err
	}

	// The browser offers every passkey it has for the instance, so nobody has to say who they are first
	assertion, session, err := rp.BeginDiscoverableLogin()
	if err != nil {
		return nil, http.Cookie{}, fmt.Errorf("failed to begin passkey sign-in: %w", err)
	}
	return s.startCeremony(ctx, ceremonySignIn, passkeyCeremony{Session: *session, Redirect: safeRedirect(redirect)}, assertion.Response)
}

// FinishPasskeySignIn checks the browser's answer and signs in the account the passkey belongs to, returning the session cookie and where to go next
func (s *Service) FinishPasskeySignIn(ctx context.Context, ceremonyToken string, response []byte) (http.Cookie, string, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return http.Cookie{}, "", err
	}
	c, err := s.takeCeremony(ctx, ceremonySignIn, ceremonyToken)
	if err != nil {
		return http.Cookie{}, "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return http.Cookie{}, "", apperror.LoginFailed(err)
	}

	// The passkey names its account, and a database failure while looking it up is not the passkey's fault
	var account passkeyAccount
	var lookupErr error
	_, credential, err := rp.ValidatePasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		account, lookupErr = s.passkeyAccountOf(ctx, rawID, userHandle)
		return account, lookupErr
	}, c.Session, parsed)
	if lookupErr != nil && !apperror.IsCode(lookupErr, apperror.CodeLoginFailed) {
		return http.Cookie{}, "", lookupErr
	} else if err != nil {
		return http.Cookie{}, "", apperror.LoginFailed(err)
	}

	// A signature counter that went backwards means the passkey was copied, so neither copy signs in any more
	if credential.Authenticator.CloneWarning {
		return http.Cookie{}, "", apperror.LoginFailed(errors.New("the passkey's signature counter went backwards"))
	}

	// The credential record changes with every use, such as its signature counter and backup state
	err = s.savePasskeyUse(ctx, account, credential)
	if err != nil {
		return http.Cookie{}, "", err
	}
	err = s.queries.TouchLogin(ctx, authdb.TouchLoginParams{ID: account.user.ID, Now: new(database.Now())})
	if err != nil {
		return http.Cookie{}, "", fmt.Errorf("failed to record sign-in: %w", err)
	}
	return s.startLogin(ctx, account.user, PasskeyProviderID, c.Redirect)
}

// passkeyAccountOf loads the account a passkey belongs to, which has to be the account its authenticator names
func (s *Service) passkeyAccountOf(ctx context.Context, rawID, userHandle []byte) (passkeyAccount, error) {
	stored, err := s.queries.GetPasskeyByCredentialID(ctx, base64.RawURLEncoding.EncodeToString(rawID))
	if database.IsNotFound(err) {
		return passkeyAccount{}, apperror.LoginFailed(errors.New("the passkey is not registered"))
	} else if err != nil {
		return passkeyAccount{}, fmt.Errorf("failed to load passkey: %w", err)
	}
	if stored.UserID != string(userHandle) {
		return passkeyAccount{}, apperror.LoginFailed(errors.New("the passkey belongs to another account than its authenticator says"))
	}
	return s.loadPasskeyAccount(ctx, stored.UserID)
}

// loadPasskeyAccount loads a passkey account with all of its passkeys
func (s *Service) loadPasskeyAccount(ctx context.Context, userID string) (passkeyAccount, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return passkeyAccount{}, err
	}
	if user.Issuer != passkeyIssuer {
		return passkeyAccount{}, apperror.Conflict("Your account signs in through " + s.providerName(user.Issuer) + ", which manages how you sign in")
	}
	rows, err := s.queries.ListUserPasskeys(ctx, userID)
	if err != nil {
		return passkeyAccount{}, fmt.Errorf("failed to load passkeys: %w", err)
	}
	account := passkeyAccount{user: user, passkeys: rows}
	for _, row := range rows {
		var credential webauthn.Credential
		err := json.Unmarshal([]byte(row.Credential), &credential)
		if err != nil {
			return passkeyAccount{}, fmt.Errorf("failed to decode passkey %s: %w", row.ID, err)
		}
		account.credentials = append(account.credentials, credential)
	}
	return account, nil
}

// savePasskeyUse stores the credential record a sign-in updated
func (s *Service) savePasskeyUse(ctx context.Context, account passkeyAccount, credential *webauthn.Credential) error {
	id := base64.RawURLEncoding.EncodeToString(credential.ID)
	for _, row := range account.passkeys {
		if row.CredentialID != id {
			continue
		}
		record, err := json.Marshal(credential)
		if err != nil {
			return fmt.Errorf("failed to encode passkey: %w", err)
		}
		err = s.queries.UsePasskey(ctx, authdb.UsePasskeyParams{ID: row.ID, Credential: string(record), Now: new(database.Now())})
		if err != nil {
			return fmt.Errorf("failed to update passkey: %w", err)
		}
		return nil
	}
	return fmt.Errorf("signed in with passkey %s, which the account doesn't have", id)
}

// BeginPasskeySignUp returns the options that create the first passkey of a new account and the cookie of its ceremony
// Only the first account of an instance and someone with an invite link may sign up, everyone else gets an account from an instance admin
func (s *Service) BeginPasskeySignUp(ctx context.Context, in SignUp) (json.RawMessage, http.Cookie, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return nil, http.Cookie{}, err
	}
	redirect := safeRedirect(in.Redirect)
	open, err := s.SetupOpen(ctx)
	if err != nil {
		return nil, http.Cookie{}, err
	}
	if !open {
		err = s.checkInvite(ctx, redirect)
		if err != nil {
			return nil, http.Cookie{}, err
		}
	}

	// The account doesn't exist until the passkey does, so a sign-up the person abandons leaves nothing behind
	c := passkeyCeremony{UserID: database.NewID(), Name: in.Name, Email: in.Email, Redirect: redirect}
	account := passkeyAccount{user: authdb.User{ID: c.UserID, Name: nonEmpty(in.Name), Email: nonEmpty(in.Email)}}
	return s.beginRegistration(ctx, rp, ceremonySignUp, c, account)
}

// FinishPasskeySignUp checks the browser's answer, creates the account with its passkey and signs it in, returning the session cookie and where to go next
func (s *Service) FinishPasskeySignUp(ctx context.Context, ceremonyToken string, response []byte) (http.Cookie, string, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return http.Cookie{}, "", err
	}
	c, err := s.takeCeremony(ctx, ceremonySignUp, ceremonyToken)
	if err != nil {
		return http.Cookie{}, "", err
	}
	account := passkeyAccount{user: authdb.User{ID: c.UserID, Name: nonEmpty(c.Name), Email: nonEmpty(c.Email)}}
	credential, err := s.createCredential(rp, account, c.Session, response)
	if err != nil {
		return http.Cookie{}, "", err
	}

	// An invite link has to still work, since it is what lets this person in once the instance has an account
	_, viaInvite := inviteToken(c.Redirect)
	if viaInvite {
		err = s.checkInvite(ctx, c.Redirect)
		if err != nil {
			return http.Cookie{}, "", err
		}
	}

	var user authdb.User
	err = s.db.InTx(ctx, func(tx *database.Tx) error {
		q := authdb.New(tx)

		// Concurrent sign-ups take turns, so only one of them creates the first account, which becomes an instance admin
		err := q.LockSetup(ctx)
		if err != nil {
			return fmt.Errorf("failed to lock setup: %w", err)
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("failed to count users: %w", err)
		}
		first := n == 0
		if !first && !viaInvite {
			return apperror.Conflict("Someone set up this instance in the meantime, ask them for an account")
		}

		user, err = q.CreateLocalUser(ctx, authdb.CreateLocalUserParams{
			ID: c.UserID, Issuer: passkeyIssuer, Name: nonEmpty(c.Name), Email: nonEmpty(c.Email), IsAdmin: first, Now: database.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}
		_, err = createPasskey(ctx, q, user.ID, credential)
		return err
	})
	if err != nil {
		return http.Cookie{}, "", err
	}

	// The first account lands in the default workspace, which it then owns, and one from an invite link joins the invite's workspace
	err = s.queries.TouchLogin(ctx, authdb.TouchLoginParams{ID: user.ID, Now: new(database.Now())})
	if err != nil {
		return http.Cookie{}, "", fmt.Errorf("failed to record sign-in: %w", err)
	}
	return s.startLogin(ctx, user, PasskeyProviderID, c.Redirect)
}

// checkInvite fails unless the redirect is the page of an invite link that still works
func (s *Service) checkInvite(ctx context.Context, redirect string) error {
	token, ok := inviteToken(redirect)
	if !ok {
		return apperror.Forbidden("Ask an instance admin for an account, or for an invite to a workspace")
	}
	return s.workspaces.CheckInvite(ctx, token)
}

// inviteToken returns the token of the invite page a redirect leads to
func inviteToken(redirect string) (string, bool) {
	token, ok := strings.CutPrefix(redirect, workspaces.InvitePath)
	return token, ok && token != ""
}

// BeginAddPasskey returns the options that create another passkey of a passkey account and the cookie of its ceremony
func (s *Service) BeginAddPasskey(ctx context.Context, userID string) (json.RawMessage, http.Cookie, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return nil, http.Cookie{}, err
	}
	account, err := s.loadPasskeyAccount(ctx, userID)
	if err != nil {
		return nil, http.Cookie{}, err
	}
	return s.beginRegistration(ctx, rp, ceremonyAdd, passkeyCeremony{UserID: userID}, account)
}

// FinishAddPasskey checks the browser's answer and adds the passkey to the account
func (s *Service) FinishAddPasskey(ctx context.Context, userID, ceremonyToken string, response []byte) (authdb.Passkey, error) {
	rp, err := s.relyingParty()
	if err != nil {
		return authdb.Passkey{}, err
	}
	c, err := s.takeCeremony(ctx, ceremonyAdd, ceremonyToken)
	if err != nil {
		return authdb.Passkey{}, err
	}

	// The ceremony is tied to the account that started it, so a session that changed hands in between can't finish it
	if c.UserID != userID {
		return authdb.Passkey{}, apperror.PasskeyFailed(errors.New("the passkey ceremony was started by another account"))
	}
	account, err := s.loadPasskeyAccount(ctx, userID)
	if err != nil {
		return authdb.Passkey{}, err
	}
	credential, err := s.createCredential(rp, account, c.Session, response)
	if err != nil {
		return authdb.Passkey{}, err
	}
	return createPasskey(ctx, s.queries, userID, credential)
}

// beginRegistration hands out the options that create a passkey of the account and keeps the ceremony
func (s *Service) beginRegistration(ctx context.Context, rp *webauthn.WebAuthn, kind string, c passkeyCeremony, account passkeyAccount) (json.RawMessage, http.Cookie, error) {
	// An authenticator that holds one of the account's passkeys already isn't asked for another, and credProps tells some password managers to keep the passkey
	creation, session, err := rp.BeginRegistration(account,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(account.credentials).CredentialDescriptors()),
		webauthn.WithExtensions(webauthn.WithExtensionCredProps()),
	)
	if err != nil {
		return nil, http.Cookie{}, fmt.Errorf("failed to begin passkey registration: %w", err)
	}
	c.Session = *session
	return s.startCeremony(ctx, kind, c, creation.Response)
}

// createCredential checks the browser's answer to a registration and returns the new credential
func (s *Service) createCredential(rp *webauthn.WebAuthn, account passkeyAccount, session webauthn.SessionData, response []byte) (*webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return nil, apperror.PasskeyFailed(err)
	}
	credential, err := rp.CreateCredential(account, session, parsed)
	if err != nil {
		return nil, apperror.PasskeyFailed(err)
	}
	return credential, nil
}

// createPasskey stores a new passkey of the user under the default name, which the user renames to tell their passkeys apart
func createPasskey(ctx context.Context, q *authdb.Queries, userID string, credential *webauthn.Credential) (authdb.Passkey, error) {
	record, err := json.Marshal(credential)
	if err != nil {
		return authdb.Passkey{}, fmt.Errorf("failed to encode passkey: %w", err)
	}
	passkey, err := q.CreatePasskey(ctx, authdb.CreatePasskeyParams{
		ID:           database.NewID(),
		UserID:       userID,
		CredentialID: base64.RawURLEncoding.EncodeToString(credential.ID),
		Name:         defaultPasskeyName,
		Credential:   string(record),
		CreatedAt:    database.Now(),
	})
	if database.IsUniqueViolation(err) {
		return authdb.Passkey{}, apperror.Conflict("This passkey is registered already")
	} else if err != nil {
		return authdb.Passkey{}, fmt.Errorf("failed to store passkey: %w", err)
	}
	return passkey, nil
}

// ListPasskeys returns the passkeys of a passkey account, oldest first
func (s *Service) ListPasskeys(ctx context.Context, userID string) ([]authdb.Passkey, error) {
	account, err := s.loadPasskeyAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	return account.passkeys, nil
}

// RenamePasskey changes the name a passkey is listed under
func (s *Service) RenamePasskey(ctx context.Context, userID, id, name string) error {
	n, err := s.queries.RenamePasskey(ctx, authdb.RenamePasskeyParams{ID: id, UserID: userID, Name: name})
	if err != nil {
		return fmt.Errorf("failed to rename passkey: %w", err)
	}
	if n == 0 {
		return apperror.NotFound("Passkey")
	}
	return nil
}

// DeletePasskey removes a passkey of the account, which keeps at least one, since only an instance admin's sign-in link gets an account without passkeys back in
func (s *Service) DeletePasskey(ctx context.Context, userID, id string) error {
	return s.db.InTx(ctx, func(tx *database.Tx) error {
		q := authdb.New(tx)
		n, err := q.DeletePasskey(ctx, authdb.DeletePasskeyParams{ID: id, UserID: userID})
		if err != nil {
			return fmt.Errorf("failed to delete passkey: %w", err)
		}
		if n == 0 {
			return apperror.NotFound("Passkey")
		}
		left, err := q.CountUserPasskeys(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to count passkeys: %w", err)
		}
		if left == 0 {
			return apperror.Conflict("You can't remove your only passkey, add another one first")
		}
		return nil
	})
}

// UpdateProfile changes the name and email address of a passkey account, which no sign-in provider keeps up to date
func (s *Service) UpdateProfile(ctx context.Context, userID, name, email string) (authdb.User, error) {
	user, err := s.queries.UpdateLocalUser(ctx, authdb.UpdateLocalUserParams{ID: userID, Issuer: passkeyIssuer, Name: nonEmpty(name), Email: nonEmpty(email)})
	if database.IsNotFound(err) {
		return authdb.User{}, apperror.Conflict("Your sign-in provider manages your name and email address")
	} else if err != nil {
		return authdb.User{}, fmt.Errorf("failed to update user: %w", err)
	}
	return user, nil
}

// CreatePasskeyUser creates a passkey account and a sign-in link through which its owner adds their first passkey
func (s *Service) CreatePasskeyUser(ctx context.Context, name, email string, admin bool) (authdb.User, SignInLink, error) {
	_, err := s.relyingParty()
	if err != nil {
		return authdb.User{}, SignInLink{}, err
	}

	// An instance admin typed in the address, which vouches for it like a sign-in provider does, so invites sent to it reach the account
	user, err := s.queries.CreateLocalUser(ctx, authdb.CreateLocalUserParams{
		ID: database.NewID(), Issuer: passkeyIssuer, Name: nonEmpty(name), Email: nonEmpty(email), EmailVerified: email != "", IsAdmin: admin, Now: database.Now(),
	})
	if err != nil {
		return authdb.User{}, SignInLink{}, fmt.Errorf("failed to create user: %w", err)
	}
	link, err := s.CreateSignInLink(ctx, user.ID)
	if err != nil {
		return authdb.User{}, SignInLink{}, err
	}
	return user, link, nil
}

// CreateSignInLink gives a passkey account a new sign-in link, which replaces the ones it had
func (s *Service) CreateSignInLink(ctx context.Context, userID string) (SignInLink, error) {
	_, err := s.relyingParty()
	if err != nil {
		return SignInLink{}, err
	}
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return SignInLink{}, err
	}
	if user.Issuer != passkeyIssuer {
		return SignInLink{}, apperror.Conflict("The user signs in through " + s.providerName(user.Issuer) + ", only passkey accounts get sign-in links")
	}
	if user.DisabledAt != nil {
		return SignInLink{}, apperror.Conflict("The user is deactivated, reactivate them first")
	}

	// Only the hash of the token is stored, so the table alone can't be turned into links
	token := crypto.RandomToken(32)
	expiresAt := time.Now().Add(signInLinkTTL).UnixMilli()
	err = s.db.InTx(ctx, func(tx *database.Tx) error {
		q := authdb.New(tx)
		now := database.Now()
		err := q.DeleteExpiredSignInLinks(ctx, now)
		if err != nil {
			return fmt.Errorf("failed to delete expired sign-in links: %w", err)
		}
		err = q.DeleteUserSignInLinks(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to replace sign-in links: %w", err)
		}
		err = q.CreateSignInLink(ctx, authdb.CreateSignInLinkParams{TokenHash: crypto.HashToken(token), UserID: userID, CreatedAt: now, ExpiresAt: expiresAt})
		if err != nil {
			return fmt.Errorf("failed to create sign-in link: %w", err)
		}
		return nil
	})
	if err != nil {
		return SignInLink{}, err
	}
	return SignInLink{URL: s.appURL + SignInLinkPath + token, ExpiresAt: expiresAt}, nil
}

// UseSignInLink signs in the account of a sign-in link, which uses the link up, and returns the session cookie and where to go next
func (s *Service) UseSignInLink(ctx context.Context, token string) (http.Cookie, string, error) {
	_, err := s.relyingParty()
	if err != nil {
		return http.Cookie{}, "", err
	}
	userID, err := s.queries.TakeSignInLink(ctx, authdb.TakeSignInLinkParams{TokenHash: crypto.HashToken(token), Now: database.Now()})
	if database.IsNotFound(err) {
		return http.Cookie{}, "", apperror.NotFound("Sign-in link")
	} else if err != nil {
		return http.Cookie{}, "", fmt.Errorf("failed to use sign-in link: %w", err)
	}
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return http.Cookie{}, "", err
	}
	err = s.queries.TouchLogin(ctx, authdb.TouchLoginParams{ID: user.ID, Now: new(database.Now())})
	if err != nil {
		return http.Cookie{}, "", fmt.Errorf("failed to record sign-in: %w", err)
	}
	return s.startLogin(ctx, user, PasskeyProviderID, accountPath)
}

// startCeremony keeps the ceremony until the browser answers, and returns the options for the browser and the cookie that names the ceremony
func (s *Service) startCeremony(ctx context.Context, kind string, c passkeyCeremony, options any) (json.RawMessage, http.Cookie, error) {
	// Ceremonies nobody finished go whenever a new one starts, which keeps the table to the ones still waiting
	err := s.queries.DeleteExpiredPasskeyCeremonies(ctx, database.Now())
	if err != nil {
		return nil, http.Cookie{}, fmt.Errorf("failed to delete expired passkey ceremonies: %w", err)
	}

	data, err := json.Marshal(c)
	if err != nil {
		return nil, http.Cookie{}, fmt.Errorf("failed to encode passkey ceremony: %w", err)
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, http.Cookie{}, fmt.Errorf("failed to encode passkey options: %w", err)
	}

	// Only the hash of the token is stored, like for sessions
	token := crypto.RandomToken(32)
	err = s.queries.CreatePasskeyCeremony(ctx, authdb.CreatePasskeyCeremonyParams{
		TokenHash: crypto.HashToken(token), Kind: kind, Data: string(data), ExpiresAt: time.Now().Add(passkeyCeremonyTTL).UnixMilli(),
	})
	if err != nil {
		return nil, http.Cookie{}, fmt.Errorf("failed to store passkey ceremony: %w", err)
	}
	return encoded, s.codec.cookie(passkeyCookieName, token, passkeyCookiePath, passkeyCeremonyTTL), nil
}

// takeCeremony ends the ceremony the cookie names and returns what it remembered, which only works once and only for the kind it was started as
func (s *Service) takeCeremony(ctx context.Context, kind, token string) (passkeyCeremony, error) {
	missing := errors.New("the passkey ceremony is missing, expired or of another kind")
	if token == "" {
		return passkeyCeremony{}, apperror.PasskeyFailed(missing)
	}
	data, err := s.queries.TakePasskeyCeremony(ctx, authdb.TakePasskeyCeremonyParams{TokenHash: crypto.HashToken(token), Kind: kind, Now: database.Now()})
	if database.IsNotFound(err) {
		return passkeyCeremony{}, apperror.PasskeyFailed(missing)
	} else if err != nil {
		return passkeyCeremony{}, fmt.Errorf("failed to load passkey ceremony: %w", err)
	}
	var c passkeyCeremony
	err = json.Unmarshal([]byte(data), &c)
	if err != nil {
		return passkeyCeremony{}, fmt.Errorf("failed to decode passkey ceremony: %w", err)
	}
	return c, nil
}

// ClearPasskeyCookie returns the cookie that forgets a finished ceremony
func (s *Service) ClearPasskeyCookie() http.Cookie {
	return s.codec.expired(passkeyCookieName, passkeyCookiePath)
}

func firstOf(values ...*string) string {
	for _, v := range values {
		if v != nil && *v != "" {
			return *v
		}
	}
	return ""
}
