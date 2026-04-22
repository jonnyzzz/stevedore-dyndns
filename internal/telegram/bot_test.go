package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type fakeAPI struct {
	mu        sync.Mutex
	updates   [][]Update
	sent      []sentMessage
	deleted   []deletedMessage
	username  string
	cancel    context.CancelFunc
	nextMsgID int64
}

type sentMessage struct {
	ChatID    int64
	Text      string
	MessageID int64
}

type deletedMessage struct {
	ChatID    int64
	MessageID int64
}

func (f *fakeAPI) GetMe(ctx context.Context) (string, error) {
	return f.username, nil
}

func (f *fakeAPI) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.updates) == 0 {
		// End-of-queue: cancel the bot's root context so Run() returns.
		if f.cancel != nil {
			f.cancel()
		}
		return nil, context.Canceled
	}
	batch := f.updates[0]
	f.updates = f.updates[1:]
	return batch, nil
}

func (f *fakeAPI) SendMessage(ctx context.Context, chatID int64, text string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextMsgID++
	id := f.nextMsgID
	f.sent = append(f.sent, sentMessage{ChatID: chatID, Text: text, MessageID: id})
	return id, nil
}

func (f *fakeAPI) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, deletedMessage{ChatID: chatID, MessageID: messageID})
	return nil
}

type fakeHandlers struct {
	status   []Binding
	rotateIn string
	rotated  Binding
	notify   []Binding
	rotErr   error
}

func (h *fakeHandlers) Status() []Binding { return h.status }
func (h *fakeHandlers) Rotate(subdomain string) (Binding, error) {
	h.rotateIn = subdomain
	if h.rotErr != nil {
		return Binding{}, h.rotErr
	}
	return h.rotated, nil
}
func (h *fakeHandlers) NotifyRotated(b Binding) { h.notify = append(h.notify, b) }

func runOnce(t *testing.T, api *fakeAPI, handlers *fakeHandlers, allow []int64) *Bot {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	api.cancel = cancel
	b := NewBot(api, handlers, []int64{}, allow, nil, nil, nil)
	_ = b.Run(ctx) // returns once api cancels ctx after draining updates
	return b
}

func TestBot_Dispatch_AllowedUserGetsStatus(t *testing.T) {
	handlers := &fakeHandlers{
		status: []Binding{
			{Subdomain: "mtp", FQDN: "mtp.example.com", Fingerprint: "abcd1234", TelegramURL: "tg://proxy?x"},
		},
	}
	api := &fakeAPI{
		username: "testbot",
		updates: [][]Update{{{
			ID: 1,
			Message: &Message{
				ID:   10,
				From: &User{ID: 42, Username: "admin"},
				Chat: Chat{ID: 42, Type: "private"},
				Text: "/status",
			},
		}}},
	}
	runOnce(t, api, handlers, []int64{42})

	if len(api.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d: %+v", len(api.sent), api.sent)
	}
	if api.sent[0].ChatID != 42 {
		t.Errorf("reply chat_id = %d, want 42", api.sent[0].ChatID)
	}
	if !strings.Contains(api.sent[0].Text, "mtp.example.com") {
		t.Errorf("status reply missing fqdn: %q", api.sent[0].Text)
	}
}

func TestBot_Dispatch_RejectsNonAllowedUser(t *testing.T) {
	api := &fakeAPI{updates: [][]Update{{{
		ID: 2,
		Message: &Message{
			ID:   11,
			From: &User{ID: 99},
			Chat: Chat{ID: 99, Type: "private"},
			Text: "/status",
		},
	}}}}
	handlers := &fakeHandlers{}
	runOnce(t, api, handlers, []int64{42})

	if len(api.sent) != 0 {
		t.Fatalf("bot replied to rejected user: %+v", api.sent)
	}
}

func TestBot_Dispatch_IgnoresGroupMessage(t *testing.T) {
	api := &fakeAPI{updates: [][]Update{{{
		ID: 3,
		Message: &Message{
			ID:   12,
			From: &User{ID: 42},
			Chat: Chat{ID: -100, Type: "supergroup"},
			Text: "/status",
		},
	}}}}
	handlers := &fakeHandlers{}
	runOnce(t, api, handlers, []int64{42})

	if len(api.sent) != 0 {
		t.Fatalf("bot replied in group chat: %+v", api.sent)
	}
}

func TestBot_Dispatch_RotateCallsHandlerAndNotifies(t *testing.T) {
	restarted := 0
	handlers := &fakeHandlers{
		rotated: Binding{Subdomain: "mtp", FQDN: "mtp.example.com", Fingerprint: "f00", TelegramURL: "tg://proxy?y"},
	}
	api := &fakeAPI{updates: [][]Update{{{
		ID: 4,
		Message: &Message{
			ID:   13,
			From: &User{ID: 42},
			Chat: Chat{ID: 42, Type: "private"},
			Text: "/rotate mtp",
		},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	api.cancel = cancel
	b := NewBot(api, handlers, nil, []int64{42}, nil, func() { restarted++ }, nil)
	_ = b.Run(ctx)

	if handlers.rotateIn != "mtp" {
		t.Errorf("Rotate called with %q, want mtp", handlers.rotateIn)
	}
	if len(handlers.notify) != 1 || handlers.notify[0].FQDN != "mtp.example.com" {
		t.Errorf("NotifyRotated not invoked correctly: %+v", handlers.notify)
	}
	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "tg://proxy?y") {
		t.Errorf("rotate reply missing new link: %+v", api.sent)
	}
}

func TestBot_Dispatch_RotateErrorSurfacedToUser(t *testing.T) {
	handlers := &fakeHandlers{rotErr: errors.New("no such subdomain")}
	api := &fakeAPI{updates: [][]Update{{{
		ID: 5,
		Message: &Message{
			ID:   14,
			From: &User{ID: 42},
			Chat: Chat{ID: 42, Type: "private"},
			Text: "/rotate missing",
		},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	api.cancel = cancel
	b := NewBot(api, handlers, nil, []int64{42}, nil, nil, nil)
	_ = b.Run(ctx)

	if len(api.sent) != 1 || !strings.Contains(api.sent[0].Text, "no such subdomain") {
		t.Errorf("error not propagated to user: %+v", api.sent)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in        string
		wantCmd   string
		wantArg   string
	}{
		{"/status", "/status", ""},
		{"/status@mybot", "/status", ""},
		{"/rotate mtp", "/rotate", "mtp"},
		{"/rotate@mybot mtp", "/rotate", "mtp"},
		{"hello", "", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.in), func(t *testing.T) {
			c, a := parseCommand(tt.in)
			if c != tt.wantCmd || a != tt.wantArg {
				t.Errorf("parseCommand(%q) = (%q, %q), want (%q, %q)", tt.in, c, a, tt.wantCmd, tt.wantArg)
			}
		})
	}
}

func TestPost_DeletesPriorAndRecordsNewPerKind(t *testing.T) {
	api := &fakeAPI{}
	handlers := &fakeHandlers{}
	store, err := NewMessageStore(t.TempDir() + "/last.json")
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}
	b := NewBot(api, handlers, []int64{100, 200}, nil, nil, nil, store)

	// First post of kind "binding:a": nothing to delete.
	b.Post(context.Background(), "binding:a", "A first")
	if len(api.sent) != 2 || len(api.deleted) != 0 {
		t.Fatalf("first post: sent=%d deleted=%d", len(api.sent), len(api.deleted))
	}
	firstA100 := api.sent[0].MessageID
	firstA200 := api.sent[1].MessageID

	// Post of a DIFFERENT kind must NOT delete the "a" messages.
	b.Post(context.Background(), "binding:b", "B first")
	if len(api.deleted) != 0 {
		t.Fatalf("different kind should not delete prior: %+v", api.deleted)
	}

	// Second post of kind "binding:a": should delete ONLY the "a" messages.
	b.Post(context.Background(), "binding:a", "A second")
	if len(api.deleted) != 2 {
		t.Fatalf("expected 2 deletes, got %d: %+v", len(api.deleted), api.deleted)
	}
	if api.deleted[0].ChatID != 100 || api.deleted[0].MessageID != firstA100 {
		t.Errorf("first delete wrong: %+v", api.deleted[0])
	}
	if api.deleted[1].ChatID != 200 || api.deleted[1].MessageID != firstA200 {
		t.Errorf("second delete wrong: %+v", api.deleted[1])
	}
	// Store for kind "binding:a" must reflect the newest IDs.
	if got := store.Get(100, "binding:a"); got != api.sent[len(api.sent)-2].MessageID {
		t.Errorf("store[100,a] = %d, want %d", got, api.sent[len(api.sent)-2].MessageID)
	}
	// Kind "binding:b" must be untouched.
	if got := store.Get(100, "binding:b"); got == 0 {
		t.Errorf("kind b must still be tracked")
	}
}

func TestPost_FallsBackToBroadcastWithoutStore(t *testing.T) {
	api := &fakeAPI{}
	handlers := &fakeHandlers{}
	b := NewBot(api, handlers, []int64{42}, nil, nil, nil, nil)

	b.Post(context.Background(), "binding:a", "first")
	b.Post(context.Background(), "binding:a", "second")

	if len(api.deleted) != 0 {
		t.Errorf("expected no deletes without a store, got %d", len(api.deleted))
	}
	if len(api.sent) != 2 {
		t.Errorf("expected 2 sends, got %d", len(api.sent))
	}
}

func TestMessageStore_RoundTrip(t *testing.T) {
	path := t.TempDir() + "/last.json"
	s, err := NewMessageStore(path)
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}
	if err := s.Set(42, "binding:a", 1001); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(42, "binding:b", 1002); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	if err := s.Set(-100, "rotation", 5); err != nil {
		t.Fatalf("Set negative: %v", err)
	}

	// Reload from disk.
	s2, err := NewMessageStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := s2.Get(42, "binding:a"); got != 1001 {
		t.Errorf("Get(42,a) = %d, want 1001", got)
	}
	if got := s2.Get(42, "binding:b"); got != 1002 {
		t.Errorf("Get(42,b) = %d, want 1002", got)
	}
	if got := s2.Get(-100, "rotation"); got != 5 {
		t.Errorf("Get(-100,rotation) = %d, want 5", got)
	}
	if got := s2.Get(42, "unknown-kind"); got != 0 {
		t.Errorf("Get for unknown kind should be 0, got %d", got)
	}

	if err := s2.Clear(42, "binding:a"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := s2.Get(42, "binding:a"); got != 0 {
		t.Errorf("Get(42,a) after Clear = %d, want 0", got)
	}
	if got := s2.Get(42, "binding:b"); got != 1002 {
		t.Errorf("Clear(a) must not affect b: got %d", got)
	}
}

func TestBot_IsAllowed(t *testing.T) {
	b := NewBot(&fakeAPI{}, &fakeHandlers{}, nil, []int64{0, 42}, nil, nil, nil)
	if b.isAllowed(0) {
		t.Error("zero must not be accepted as user ID (placeholder guard)")
	}
	if !b.isAllowed(42) {
		t.Error("configured user 42 must be accepted")
	}
	if b.isAllowed(7) {
		t.Error("unconfigured user 7 must not be accepted")
	}

	// Empty allow-list rejects every user.
	empty := NewBot(&fakeAPI{}, &fakeHandlers{}, nil, nil, nil, nil, nil)
	if empty.isAllowed(42) {
		t.Error("bot with no allow-list must reject every command sender")
	}
}
