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
	mu       sync.Mutex
	updates  [][]Update
	sent     []sentMessage
	username string
	cancel   context.CancelFunc
}

type sentMessage struct {
	ChatID int64
	Text   string
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

func (f *fakeAPI) SendMessage(ctx context.Context, chatID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{ChatID: chatID, Text: text})
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
	allowOverride := AllowedUsers
	AllowedUsers = allow
	t.Cleanup(func() { AllowedUsers = allowOverride })

	ctx, cancel := context.WithCancel(context.Background())
	api.cancel = cancel
	b := NewBot(api, handlers, []int64{}, nil, nil)
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
	AllowedUsers = []int64{42}
	ctx, cancel := context.WithCancel(context.Background())
	api.cancel = cancel
	b := NewBot(api, handlers, nil, nil, func() { restarted++ })
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
	AllowedUsers = []int64{42}
	ctx, cancel := context.WithCancel(context.Background())
	api.cancel = cancel
	b := NewBot(api, handlers, nil, nil, nil)
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

func TestAllowlist_IgnoresZeroPlaceholder(t *testing.T) {
	AllowedUsers = []int64{0, 42}
	if IsAllowed(0) {
		t.Error("zero (placeholder) must not be accepted as user ID")
	}
	if !IsAllowed(42) {
		t.Error("real user 42 must be accepted")
	}
	if IsAllowed(7) {
		t.Error("user 7 must not be accepted")
	}
}
