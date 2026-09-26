// Package notifications tells a webhook when runs fail, fall back to the agent or get a job demoted (PLAN.md §16, M6)
package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/italypaleale/francis/builtin/taskpool"
	francishost "github.com/italypaleale/francis/host"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/notifications/notificationsdb"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/settings"
)

// deliveryTimeout bounds one POST to the webhook
const deliveryTimeout = 10 * time.Second

// SettingsReader reads the workspace's notification settings
type SettingsReader interface {
	Get(ctx context.Context, workspaceID string) (settings.WorkspaceSettings, error)
}

// SecretExpander resolves {{secret:NAME}} references, which carry the signing secret
type SecretExpander interface {
	Expand(ctx context.Context, workspaceID, value string) (string, error)
}

// DemotionChecker tells whether repeated fallbacks took a job back to Assisted
type DemotionChecker interface {
	Demoted(ctx context.Context, workspaceID, jobID string) (bool, error)
}

// HTTPClients gives out clients whose dialer the egress guard checks
type HTTPClients interface {
	HTTPClient(timeout time.Duration) *http.Client
}

type Dependencies struct {
	DB       *database.DB
	Actors   francishost.Host
	Settings SettingsReader
	Secrets  SecretExpander
	Demotion DemotionChecker
	Egress   HTTPClients
	// AppURL builds links to runs in the messages
	AppURL string
}

type Module struct {
	deps    Dependencies
	queries *notificationsdb.Queries
	pool    *taskpool.TaskPoolService
}

func New(deps Dependencies) (*Module, error) {
	m := &Module{deps: deps, queries: notificationsdb.New(deps.DB)}

	// Deliveries are durable tasks, so a webhook that is down briefly still gets the message
	pool, err := taskpool.New("notifications",
		taskpool.WithHandler(m.handle),
		taskpool.WithConcurrency(2),
		taskpool.WithMaxAttempts(5),
		taskpool.WithLogger(slog.Default().With("scope", "notifications")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create the notifications taskpool: %w", err)
	}
	err = deps.Actors.RegisterBuiltInActor(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to register the notifications taskpool: %w", err)
	}
	m.pool = pool.Service(deps.Actors.Service())
	return m, nil
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("test-notification", http.MethodPost, "/api/settings/notifications/test", "Settings"), auth, m.test)
}

// RunFinished queues a notification for each event the finished run caused and the workspace wants to hear about
func (m *Module) RunFinished(ctx context.Context, run runner.Run, final runner.Final) {
	ws, err := m.deps.Settings.Get(ctx, run.WorkspaceID)
	if err != nil || ws.NotifyWebhookURL == nil {
		return
	}

	found := eventsFor(ws.NotifyOn, final, func() bool {
		demoted, err := m.deps.Demotion.Demoted(ctx, run.WorkspaceID, run.JobID)
		return err == nil && demoted
	})
	for _, event := range found {
		_, err := m.pool.Submit(ctx, task{WorkspaceID: run.WorkspaceID, RunID: run.ID, Event: event}, taskpool.WithTaskKey(run.ID+"-"+strings.ReplaceAll(event, ".", "-")))
		if err != nil {
			slog.WarnContext(ctx, "Failed to queue a notification", slog.String("run", run.ID), slog.String("event", event), slog.Any("error", err))
		}
	}
}

// eventsFor lists the events a finished run caused that the workspace wants to hear about
// Demotion is only checked after a fallback, since only a fallback can cause it
func eventsFor(notifyOn []string, final runner.Final, demoted func() bool) []string {
	var found []string
	if final.Status == runner.StatusFailed || final.Status == runner.StatusTimedOut {
		found = append(found, settings.EventRunFailed)
	}
	if final.FellBack {
		found = append(found, settings.EventRunFellBack)
		if slices.Contains(notifyOn, settings.EventJobDemoted) && demoted() {
			found = append(found, settings.EventJobDemoted)
		}
	}
	return slices.DeleteFunc(found, func(e string) bool { return !slices.Contains(notifyOn, e) })
}

type task struct {
	WorkspaceID string `json:"workspaceId"`
	RunID       string `json:"runId"`
	Event       string `json:"event"`
}

// errPermanent marks a delivery the webhook rejected, which another attempt won't change
var errPermanent = errors.New("the webhook rejected the notification")

func (m *Module) handle(ctx context.Context, t taskpool.Task) error {
	var in task
	if err := t.Decode(&in); err != nil {
		return err
	}
	run, err := m.queries.GetRunDetails(ctx, notificationsdb.GetRunDetailsParams{WorkspaceID: in.WorkspaceID, ID: in.RunID})
	if database.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = m.deliver(ctx, in.WorkspaceID, m.payload(in.Event, run))

	// A rejected delivery is logged instead of retried, since the webhook will reject it again
	if errors.Is(err, errPermanent) {
		slog.WarnContext(ctx, "The notification webhook rejected a delivery", slog.String("run", in.RunID), slog.String("event", in.Event), slog.Any("error", err))
		return nil
	}
	return err
}

// Payload is what the webhook receives; text and content make it work with Slack and Discord webhooks as they are
type Payload struct {
	Event     string      `json:"event"`
	Text      string      `json:"text"`
	Content   string      `json:"content"`
	Job       *PayloadJob `json:"job,omitempty"`
	Run       *PayloadRun `json:"run,omitempty"`
	Timestamp string      `json:"timestamp"`
}

type PayloadJob struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PayloadRun struct {
	ID       string `json:"id"`
	Number   int64  `json:"number"`
	Status   string `json:"status"`
	Mode     string `json:"mode"`
	Trigger  string `json:"trigger"`
	FellBack bool   `json:"fellBack"`
	Error    string `json:"error,omitempty"`
	URL      string `json:"url"`
}

func (m *Module) payload(event string, run notificationsdb.GetRunDetailsRow) Payload {
	url := strings.TrimRight(m.deps.AppURL, "/") + "/runs/" + run.ID
	errText := ""
	if run.Error != nil {
		errText = *run.Error
	}

	var text string
	switch event {
	case settings.EventRunFailed:
		text = fmt.Sprintf("%s run #%d %s", run.JobName, run.Number, strings.ReplaceAll(run.Status, "_", " "))
		if errText != "" {
			text += ": " + errText
		}
	case settings.EventRunFellBack:
		text = fmt.Sprintf("%s run #%d: the main script failed and the agent finished the job", run.JobName, run.Number)
	case settings.EventJobDemoted:
		text = fmt.Sprintf("%s was demoted to Assisted after its main script failed twice in a row", run.JobName)
	}
	text = "Umpteenth: " + text + "\n" + url
	return Payload{
		Event:   event,
		Text:    text,
		Content: text,
		Job:     &PayloadJob{ID: run.JobID, Name: run.JobName},
		Run: &PayloadRun{
			ID: run.ID, Number: run.Number, Status: run.Status, Mode: run.Mode, Trigger: run.Trigger,
			FellBack: run.FellBack, Error: errText, URL: url,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// deliver posts a payload to the workspace's webhook, signing it when a secret is configured
func (m *Module) deliver(ctx context.Context, workspaceID string, p Payload) error {
	ws, err := m.deps.Settings.Get(ctx, workspaceID)
	if err != nil {
		return err
	}
	if ws.NotifyWebhookURL == nil {
		return nil
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *ws.NotifyWebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", errPermanent, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Umpteenth")
	req.Header.Set("X-Umpteenth-Event", p.Event)

	// The signature lets the receiver check the message came from this instance, like GitHub's webhook signatures
	if ws.NotifySecret != nil && m.deps.Secrets != nil {
		secret, err := m.deps.Secrets.Expand(ctx, workspaceID, "{{secret:"+*ws.NotifySecret+"}}")
		if err != nil {
			return fmt.Errorf("%w: the signing secret %s can't be read: %w", errPermanent, *ws.NotifySecret, err)
		}
		req.Header.Set("X-Umpteenth-Signature", Sign(secret, body))
	}

	resp, err := m.deps.Egress.HTTPClient(deliveryTimeout).Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach the notification webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	// Server errors and rate limits are worth another attempt, other refusals are not
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("the notification webhook answered %s", resp.Status)
	default:
		return fmt.Errorf("%w with %s", errPermanent, resp.Status)
	}
}

// Sign computes the X-Umpteenth-Signature header value of a body
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type testOutput struct {
	Body struct {
		Delivered bool `json:"delivered"`
	}
}

// test sends a sample notification right away, so the webhook can be checked from the settings page
func (m *Module) test(ctx context.Context, _ *struct{}) (*testOutput, error) {
	wid := principal.WorkspaceID(ctx)
	ws, err := m.deps.Settings.Get(ctx, wid)
	if err != nil {
		return nil, err
	}
	if ws.NotifyWebhookURL == nil {
		return nil, apperror.InvalidField("notifyWebhookUrl", "required", "set a webhook URL first")
	}
	text := "Umpteenth: this is a test notification. Failed runs will be reported here."
	err = m.deliver(ctx, wid, Payload{Event: "test", Text: text, Content: text, Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, apperror.New(apperror.CodeProviderError, http.StatusBadGateway, "The webhook did not accept the notification: "+err.Error())
	}
	out := &testOutput{}
	out.Body.Delivered = true
	return out, nil
}
