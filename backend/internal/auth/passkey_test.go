//go:build unit

package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

// softAuthenticator is a passkey authenticator in memory, which answers ceremonies like a password manager that verified its user
type softAuthenticator struct {
	origin string
	rpID   string
	keys   map[string]*ecdsa.PrivateKey
	// handles are the user handles of the passkeys, by credential ID
	handles map[string][]byte
	counter uint32
}

func newSoftAuthenticator() *softAuthenticator {
	return &softAuthenticator{origin: testAppURL, rpID: "umpteenth.example.com", keys: map[string]*ecdsa.PrivateKey{}, handles: map[string][]byte{}}
}

var b64 = base64.RawURLEncoding

// clientData is the clientDataJSON the browser signs for a ceremony of the type
func (a *softAuthenticator) clientData(t *testing.T, kind, challenge string) []byte {
	data, err := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	require.NoError(t, err)
	return data
}

// authData is the authenticator data with user presence and verification, and the attested credential when there is one
func (a *softAuthenticator) authData(attested []byte) []byte {
	rpHash := sha256.Sum256([]byte(a.rpID))
	flags := byte(0x01 | 0x04)
	if attested != nil {
		flags |= 0x40
	}
	a.counter++
	data := append(rpHash[:], flags)
	data = binary.BigEndian.AppendUint32(data, a.counter)
	return append(data, attested...)
}

// create answers registration options with a new passkey, as navigator.credentials.create would
func (a *softAuthenticator) create(t *testing.T, options json.RawMessage) []byte {
	var opts struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	require.NoError(t, json.Unmarshal(options, &opts))

	// A new P-256 key, described as a COSE key in the attested credential data
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	credentialID := make([]byte, 16)
	_, err = rand.Read(credentialID)
	require.NoError(t, err)
	handle, err := b64.DecodeString(opts.User.ID)
	require.NoError(t, err)
	a.keys[string(credentialID)] = key
	a.handles[string(credentialID)] = handle

	pub, err := key.PublicKey.Bytes()
	require.NoError(t, err)
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: pub[1:33], -3: pub[33:]})
	require.NoError(t, err)
	attested := make([]byte, 16)
	attested = binary.BigEndian.AppendUint16(attested, 16)
	attested = append(append(attested, credentialID...), cose...)

	attestation, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(attested)})
	require.NoError(t, err)
	return a.credential(t, credentialID, map[string]any{
		"clientDataJSON":    b64.EncodeToString(a.clientData(t, "webauthn.create", opts.Challenge)),
		"attestationObject": b64.EncodeToString(attestation),
	})
}

// get answers sign-in options with the passkey, as navigator.credentials.get would
func (a *softAuthenticator) get(t *testing.T, options json.RawMessage, credentialID []byte) []byte {
	var opts struct {
		Challenge string `json:"challenge"`
	}
	require.NoError(t, json.Unmarshal(options, &opts))

	clientData := a.clientData(t, "webauthn.get", opts.Challenge)
	authData := a.authData(nil)
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.keys[string(credentialID)], digest[:])
	require.NoError(t, err)
	return a.credential(t, credentialID, map[string]any{
		"clientDataJSON":    b64.EncodeToString(clientData),
		"authenticatorData": b64.EncodeToString(authData),
		"signature":         b64.EncodeToString(signature),
		"userHandle":        b64.EncodeToString(a.handles[string(credentialID)]),
	})
}

func (a *softAuthenticator) credential(t *testing.T, credentialID []byte, response map[string]any) []byte {
	data, err := json.Marshal(map[string]any{
		"id": b64.EncodeToString(credentialID), "rawId": b64.EncodeToString(credentialID), "type": "public-key",
		"response": response, "clientExtensionResults": map[string]any{},
	})
	require.NoError(t, err)
	return data
}

// only returns the ID of the one passkey the authenticator holds
func (a *softAuthenticator) only(t *testing.T) []byte {
	require.Len(t, a.keys, 1)
	for id := range a.keys {
		return []byte(id)
	}
	return nil
}

func newPasskeyTestService(t *testing.T, workspacesEnabled bool) (*Service, *workspaces.Module) {
	db := testutil.NewDatabaseForTest(t)
	ws := workspaces.New(workspaces.Dependencies{DB: db, Enabled: workspacesEnabled})
	require.NoError(t, ws.EnsureDefault(t.Context(), false))
	m, err := New(Dependencies{
		DB:            db,
		Workspaces:    ws,
		EncryptionKey: []byte("unit-test-encryption-key"),
		Config:        Config{AppURL: testAppURL, Passkeys: true},
	})
	require.NoError(t, err)
	return m.service, ws
}

// signUp creates a passkey account on the authenticator and returns the principal of its session and where it went
func signUp(t *testing.T, svc *Service, a *softAuthenticator, in SignUp) (principal.Principal, string, error) {
	options, ceremony, err := svc.BeginPasskeySignUp(t.Context(), in)
	if err != nil {
		return principal.Principal{}, "", err
	}
	session, redirect, err := svc.FinishPasskeySignUp(t.Context(), ceremony.Value, a.create(t, options))
	if err != nil {
		return principal.Principal{}, "", err
	}
	p, err := svc.VerifySession(t.Context(), session.Value)
	require.NoError(t, err)
	return p, redirect, nil
}

// passkeySignIn signs in with the authenticator's passkey and returns the principal of the session and where it went
func passkeySignIn(t *testing.T, svc *Service, a *softAuthenticator) (principal.Principal, string, error) {
	options, ceremony, err := svc.BeginPasskeySignIn(t.Context(), "/jobs")
	require.NoError(t, err)
	session, redirect, err := svc.FinishPasskeySignIn(t.Context(), ceremony.Value, a.get(t, options, a.only(t)))
	if err != nil {
		return principal.Principal{}, "", err
	}
	p, err := svc.VerifySession(t.Context(), session.Value)
	require.NoError(t, err)
	return p, redirect, nil
}

func TestTheFirstPasskeyAccountSetsUpTheInstance(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	open, err := svc.SetupOpen(t.Context())
	require.NoError(t, err)
	require.True(t, open)

	// The first account becomes an instance admin, and with workspaces off it lands in the default workspace
	p, redirect, err := signUp(t, svc, newSoftAuthenticator(), SignUp{Name: "Ada", Email: "ada@example.com"})
	require.NoError(t, err)
	require.Equal(t, "/", redirect)
	require.True(t, p.InstanceAdmin)
	require.Equal(t, PasskeyProviderID, p.LoginProvider)
	require.Equal(t, workspaces.DefaultID, p.WorkspaceID)
	require.Equal(t, principal.RoleOwner, p.Role)

	// The account's own address isn't verified by anyone, so it picks up no invites
	user, err := svc.GetUser(t.Context(), p.UserID)
	require.NoError(t, err)
	require.False(t, user.EmailVerified)

	// After that, signing up takes an invite or an account from an instance admin
	open, err = svc.SetupOpen(t.Context())
	require.NoError(t, err)
	require.False(t, open)
	_, _, err = svc.BeginPasskeySignUp(t.Context(), SignUp{Name: "Mallory"})
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden))
}

func TestOnlyOneOfTwoRacingSetupsCreatesTheFirstAccount(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	a, b := newSoftAuthenticator(), newSoftAuthenticator()

	// Both started while the instance had no users, so the second only fails once it finishes
	optionsA, ceremonyA, err := svc.BeginPasskeySignUp(t.Context(), SignUp{Name: "A"})
	require.NoError(t, err)
	optionsB, ceremonyB, err := svc.BeginPasskeySignUp(t.Context(), SignUp{Name: "B"})
	require.NoError(t, err)
	_, _, err = svc.FinishPasskeySignUp(t.Context(), ceremonyA.Value, a.create(t, optionsA))
	require.NoError(t, err)
	_, _, err = svc.FinishPasskeySignUp(t.Context(), ceremonyB.Value, b.create(t, optionsB))
	require.True(t, apperror.IsCode(err, apperror.CodeConflict))
}

func TestPasskeysSignInTheirAccount(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	a := newSoftAuthenticator()
	created, _, err := signUp(t, svc, a, SignUp{Name: "Ada"})
	require.NoError(t, err)

	p, redirect, err := passkeySignIn(t, svc, a)
	require.NoError(t, err)
	require.Equal(t, created.UserID, p.UserID)
	require.Equal(t, "/jobs", redirect)
	require.Equal(t, PasskeyProviderID, p.LoginProvider)

	passkeys, err := svc.ListPasskeys(t.Context(), p.UserID)
	require.NoError(t, err)
	require.Len(t, passkeys, 1)
	require.NotNil(t, passkeys[0].LastUsedAt)
	require.Equal(t, "Passkey", passkeys[0].Name)

	// A deactivated account stays locked out, whatever its passkey says
	_, err = svc.db.ExecContext(t.Context(), "UPDATE users SET disabled_at = 1")
	require.NoError(t, err)
	_, _, err = passkeySignIn(t, svc, a)
	require.True(t, apperror.IsCode(err, apperror.CodeAccountDisabled))
}

func TestPasskeyCeremoniesAreAnsweredOnce(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	a := newSoftAuthenticator()
	_, _, err := signUp(t, svc, a, SignUp{Name: "Ada"})
	require.NoError(t, err)

	options, ceremony, err := svc.BeginPasskeySignIn(t.Context(), "/")
	require.NoError(t, err)
	response := a.get(t, options, a.only(t))
	_, _, err = svc.FinishPasskeySignIn(t.Context(), ceremony.Value, response)
	require.NoError(t, err)

	// Replaying the same answer finds no ceremony left to finish
	_, _, err = svc.FinishPasskeySignIn(t.Context(), ceremony.Value, response)
	require.True(t, apperror.IsCode(err, apperror.CodePasskeyFailed))

	// A ceremony only finishes the kind it was started as
	options, ceremony, err = svc.BeginPasskeySignIn(t.Context(), "/")
	require.NoError(t, err)
	_, _, err = svc.FinishPasskeySignUp(t.Context(), ceremony.Value, a.create(t, options))
	require.True(t, apperror.IsCode(err, apperror.CodePasskeyFailed))
}

func TestPasskeysOfUnknownCredentialsDontSignIn(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	a := newSoftAuthenticator()
	p, _, err := signUp(t, svc, a, SignUp{Name: "Ada"})
	require.NoError(t, err)

	// The server forgot the passkey, as after deleting it, while the authenticator still offers it
	_, err = svc.db.ExecContext(t.Context(), "DELETE FROM passkeys WHERE user_id = $1", p.UserID)
	require.NoError(t, err)
	_, _, err = passkeySignIn(t, svc, a)
	require.True(t, apperror.IsCode(err, apperror.CodeLoginFailed))
}

func TestPasskeyAccountsSignUpThroughInviteLinks(t *testing.T) {
	svc, ws := newPasskeyTestService(t, true)
	owner, _, err := signUp(t, svc, newSoftAuthenticator(), SignUp{Name: "Owner"})
	require.NoError(t, err)
	invite, err := ws.Invite(t.Context(), owner.WorkspaceID, owner.UserID, "", principal.RoleMember, time.Hour)
	require.NoError(t, err)

	// The newcomer lands in the invite's workspace without being an instance admin
	p, redirect, err := signUp(t, svc, newSoftAuthenticator(), SignUp{Name: "Joiner", Redirect: workspaces.InvitePath + invite.Token})
	require.NoError(t, err)
	require.Equal(t, owner.WorkspaceID, p.WorkspaceID)
	require.Equal(t, principal.RoleMember, p.Role)
	require.False(t, p.InstanceAdmin)
	require.NotEqual(t, workspaces.InvitePath+invite.Token, redirect)

	// The link is used up, so it doesn't sign up anyone else
	_, _, err = svc.BeginPasskeySignUp(t.Context(), SignUp{Name: "Another", Redirect: workspaces.InvitePath + invite.Token})
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound))
}

func TestSignInLinksLetNewAccountsAddPasskeys(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	_, _, err := signUp(t, svc, newSoftAuthenticator(), SignUp{Name: "Admin"})
	require.NoError(t, err)

	// An instance admin creates the account, whose address counts as verified since the admin typed it in
	user, link, err := svc.CreatePasskeyUser(t.Context(), "Grace", "grace@example.com", false)
	require.NoError(t, err)
	require.True(t, user.EmailVerified)
	require.Contains(t, link.URL, testAppURL+SignInLinkPath)
	token := link.URL[len(testAppURL+SignInLinkPath):]

	// The link signs in once and goes to the account page, where its owner adds a passkey
	session, redirect, err := svc.UseSignInLink(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, accountPath, redirect)
	p, err := svc.VerifySession(t.Context(), session.Value)
	require.NoError(t, err)
	require.Equal(t, user.ID, p.UserID)
	_, _, err = svc.UseSignInLink(t.Context(), token)
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound))

	a := newSoftAuthenticator()
	options, ceremony, err := svc.BeginAddPasskey(t.Context(), user.ID)
	require.NoError(t, err)
	_, err = svc.FinishAddPasskey(t.Context(), user.ID, ceremony.Value, a.create(t, options))
	require.NoError(t, err)
	signedIn, _, err := passkeySignIn(t, svc, a)
	require.NoError(t, err)
	require.Equal(t, user.ID, signedIn.UserID)

	// A new link replaces the one before it
	first, err := svc.CreateSignInLink(t.Context(), user.ID)
	require.NoError(t, err)
	_, err = svc.CreateSignInLink(t.Context(), user.ID)
	require.NoError(t, err)
	_, _, err = svc.UseSignInLink(t.Context(), first.URL[len(testAppURL+SignInLinkPath):])
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound))
}

func TestAccountsKeepTheirLastPasskey(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	p, _, err := signUp(t, svc, newSoftAuthenticator(), SignUp{Name: "Ada"})
	require.NoError(t, err)
	passkeys, err := svc.ListPasskeys(t.Context(), p.UserID)
	require.NoError(t, err)
	err = svc.DeletePasskey(t.Context(), p.UserID, passkeys[0].ID)
	require.True(t, apperror.IsCode(err, apperror.CodeConflict))

	// With a second passkey the first one can go, but only by its own account
	options, ceremony, err := svc.BeginAddPasskey(t.Context(), p.UserID)
	require.NoError(t, err)
	_, err = svc.FinishAddPasskey(t.Context(), p.UserID, ceremony.Value, newSoftAuthenticator().create(t, options))
	require.NoError(t, err)
	err = svc.DeletePasskey(t.Context(), "someone-else", passkeys[0].ID)
	require.True(t, apperror.IsCode(err, apperror.CodeNotFound))
	require.NoError(t, svc.DeletePasskey(t.Context(), p.UserID, passkeys[0].ID))
}

func TestProviderAccountsHaveNoPasskeys(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	cookie, _, err := svc.signIn(t.Context(), identity{Issuer: "https://id.example.com", Subject: "sso"}, "pocket-id", "/")
	require.NoError(t, err)
	p, err := svc.VerifySession(t.Context(), cookie.Value)
	require.NoError(t, err)

	_, _, err = svc.BeginAddPasskey(t.Context(), p.UserID)
	require.True(t, apperror.IsCode(err, apperror.CodeConflict))
	_, err = svc.CreateSignInLink(t.Context(), p.UserID)
	require.True(t, apperror.IsCode(err, apperror.CodeConflict))
	_, err = svc.UpdateProfile(t.Context(), p.UserID, "New name", "")
	require.True(t, apperror.IsCode(err, apperror.CodeConflict))

	// An SSO sign-in closes the setup too, so nobody claims an instance someone already uses
	open, err := svc.SetupOpen(t.Context())
	require.NoError(t, err)
	require.False(t, open)
}

func TestPasskeysCanBeTurnedOff(t *testing.T) {
	svc := newTestService(t)
	require.False(t, svc.PasskeysEnabled())
	open, err := svc.SetupOpen(t.Context())
	require.NoError(t, err)
	require.False(t, open)
	_, _, err = svc.BeginPasskeySignIn(t.Context(), "/")
	require.True(t, apperror.IsCode(err, apperror.CodeForbidden))
}

func TestPasskeyCeremonyCookieReachesEveryPasskeyEndpoint(t *testing.T) {
	svc, _ := newPasskeyTestService(t, false)
	_, cookie, err := svc.BeginPasskeySignIn(t.Context(), "/")
	require.NoError(t, err)
	require.Equal(t, "/api", cookie.Path)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
}
