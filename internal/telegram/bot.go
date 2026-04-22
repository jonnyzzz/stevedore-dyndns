package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// API client interface covers the two Telegram methods this bot needs;
// exposing an interface makes the message flow straightforward to mock.
type API interface {
	GetMe(ctx context.Context) (string, error)
	GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error)
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Update mirrors the subset of the Telegram Update object we consume.
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message,omitempty"`
}

// Message is a Telegram message.
type Message struct {
	ID   int64  `json:"message_id"`
	From *User  `json:"from,omitempty"`
	Chat Chat   `json:"chat"`
	Date int64  `json:"date"`
	Text string `json:"text,omitempty"`
}

// User is a Telegram user.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
	IsBot    bool   `json:"is_bot,omitempty"`
}

// Chat is a Telegram chat. Type is "private", "group", "supergroup", or
// "channel".
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Binding is the subset of mtproto.Binding the bot needs for /status and
// notifications. Declared here so the telegram package does not depend on
// internal/mtproto directly (the bot is handed its operational surface via
// the Handlers interface).
type Binding struct {
	Subdomain   string
	FQDN        string
	Fingerprint string
	TelegramURL string
}

// Handlers are the operations the bot invokes on the running service. All
// methods return user-facing strings plus optional errors.
type Handlers interface {
	// Status returns the current list of MTProto bindings.
	Status() []Binding
	// Rotate rotates the secret for subdomain (or returns an error with a
	// user-displayable message). Called synchronously; the bot may schedule
	// a service restart afterwards.
	Rotate(subdomain string) (Binding, error)
	// NotifyRotated is called after Rotate succeeds so the bot can push the
	// new Telegram URL to configured notification chats.
	NotifyRotated(b Binding)
}

// Bot is the long-polling Telegram bot. Start it with Run; it exits on
// context cancellation.
type Bot struct {
	api        API
	handlers   Handlers
	log        *slog.Logger
	notifyChats []int64
	// restart, when non-nil, is invoked after a successful /rotate so the
	// host can signal a clean service restart. The bot does not exit itself.
	restart func()

	mu     sync.Mutex
	offset int64
}

// NewBot constructs a bot ready for Run. The restart callback is optional.
func NewBot(api API, handlers Handlers, notifyChats []int64, log *slog.Logger, restart func()) *Bot {
	if log == nil {
		log = slog.Default()
	}
	return &Bot{
		api:         api,
		handlers:    handlers,
		log:         log,
		notifyChats: append([]int64(nil), notifyChats...),
		restart:     restart,
	}
}

// Broadcast sends text to each notification chat. Errors per chat are logged
// but don't abort the broadcast. Safe to call concurrently with Run.
func (b *Bot) Broadcast(ctx context.Context, text string) {
	for _, chatID := range b.notifyChats {
		if err := b.api.SendMessage(ctx, chatID, text); err != nil {
			b.log.Warn("telegram broadcast failed", "chat_id", chatID, "error", err)
		}
	}
}

// Run long-polls Telegram until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	botName, err := b.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram: GetMe: %w", err)
	}
	b.log.Info("Telegram bot ready", "username", botName, "notify_chats", len(b.notifyChats))

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		updates, err := b.api.GetUpdates(ctx, b.offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.log.Warn("telegram getUpdates failed", "error", err)
			// Back off on transient errors so we don't spin the CPU.
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return nil
			}
			continue
		}

		for _, u := range updates {
			b.dispatch(ctx, u)
			if u.ID >= b.offset {
				b.mu.Lock()
				b.offset = u.ID + 1
				b.mu.Unlock()
			}
		}
	}
}

func (b *Bot) dispatch(ctx context.Context, u Update) {
	if u.Message == nil || u.Message.From == nil {
		return
	}
	// Groups are write-only: acknowledge nothing.
	if u.Message.Chat.Type != "private" {
		return
	}
	// Enforce allow-list. Anonymous / rejected users see nothing.
	if !IsAllowed(u.Message.From.ID) {
		b.log.Info("ignoring non-allow-listed user",
			"user_id", u.Message.From.ID,
			"username", u.Message.From.Username)
		return
	}
	cmd, arg := parseCommand(u.Message.Text)
	switch cmd {
	case "/start", "/help":
		b.reply(ctx, u.Message.Chat.ID,
			"dyndns bot. Commands:\n"+
				"/status — list MTProto bindings\n"+
				"/rotate <subdomain> — rotate a subdomain's secret (service restart)")
	case "/status":
		b.handleStatus(ctx, u.Message.Chat.ID)
	case "/rotate":
		b.handleRotate(ctx, u.Message.Chat.ID, arg)
	default:
		if cmd != "" {
			b.reply(ctx, u.Message.Chat.ID, "unknown command: "+cmd)
		}
	}
}

func (b *Bot) handleStatus(ctx context.Context, chatID int64) {
	bindings := b.handlers.Status()
	if len(bindings) == 0 {
		b.reply(ctx, chatID, "No MTProto bindings configured.")
		return
	}
	var sb strings.Builder
	sb.WriteString("MTProto bindings:\n")
	for _, bnd := range bindings {
		fmt.Fprintf(&sb, "• %s  fp=%s\n  %s\n",
			bnd.FQDN, bnd.Fingerprint, bnd.TelegramURL)
	}
	b.reply(ctx, chatID, sb.String())
}

func (b *Bot) handleRotate(ctx context.Context, chatID int64, arg string) {
	sub := strings.TrimSpace(arg)
	if sub == "" {
		b.reply(ctx, chatID, "usage: /rotate <subdomain>")
		return
	}
	bnd, err := b.handlers.Rotate(sub)
	if err != nil {
		b.reply(ctx, chatID, "rotate failed: "+err.Error())
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf(
		"Rotated %s (fp=%s).\nNew link:\n%s\nService is restarting to apply the new secret.",
		bnd.FQDN, bnd.Fingerprint, bnd.TelegramURL))
	b.handlers.NotifyRotated(bnd)
	if b.restart != nil {
		// Slight delay so the message hits the wire before the process exits.
		go func() {
			time.Sleep(1 * time.Second)
			b.restart()
		}()
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	if err := b.api.SendMessage(ctx, chatID, text); err != nil {
		b.log.Warn("telegram reply failed", "chat_id", chatID, "error", err)
	}
}

// parseCommand splits a Telegram message into command + rest-of-text. The
// command is the first whitespace-delimited token; the remainder is the
// argument. Only messages starting with "/" are recognized as commands.
func parseCommand(text string) (cmd, arg string) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") {
		return "", ""
	}
	idx := strings.IndexAny(t, " \t\n")
	if idx < 0 {
		// Strip @botname suffix if present ("/status@mybot" → "/status").
		if at := strings.Index(t, "@"); at > 0 {
			return t[:at], ""
		}
		return t, ""
	}
	cmd = t[:idx]
	if at := strings.Index(cmd, "@"); at > 0 {
		cmd = cmd[:at]
	}
	arg = strings.TrimSpace(t[idx+1:])
	return cmd, arg
}

// --------------------------------------------------------------------------

// HTTPAPI is the production Telegram Bot API client.
type HTTPAPI struct {
	token  string
	client *http.Client
}

// NewHTTPAPI wraps an *http.Client (or uses a default one) with the given
// bot token. A 35-second timeout covers Telegram's 30-second long-poll plus
// a safety margin.
func NewHTTPAPI(token string, client *http.Client) *HTTPAPI {
	if client == nil {
		client = &http.Client{Timeout: 35 * time.Second}
	}
	return &HTTPAPI{token: token, client: client}
}

func (a *HTTPAPI) GetMe(ctx context.Context) (string, error) {
	var result struct {
		Ok     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description,omitempty"`
	}
	if err := a.call(ctx, "getMe", nil, &result); err != nil {
		return "", err
	}
	if !result.Ok {
		return "", fmt.Errorf("telegram getMe: %s", result.Description)
	}
	return result.Result.Username, nil
}

func (a *HTTPAPI) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	params := url.Values{}
	params.Set("offset", fmt.Sprintf("%d", offset))
	params.Set("timeout", fmt.Sprintf("%d", timeout))
	params.Set("allowed_updates", `["message"]`)

	var result struct {
		Ok          bool     `json:"ok"`
		Result      []Update `json:"result"`
		Description string   `json:"description,omitempty"`
	}
	if err := a.call(ctx, "getUpdates", params, &result); err != nil {
		return nil, err
	}
	if !result.Ok {
		return nil, fmt.Errorf("telegram getUpdates: %s", result.Description)
	}
	return result.Result, nil
}

func (a *HTTPAPI) SendMessage(ctx context.Context, chatID int64, text string) error {
	params := url.Values{}
	params.Set("chat_id", fmt.Sprintf("%d", chatID))
	params.Set("text", text)
	params.Set("disable_web_page_preview", "true")

	var result struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description,omitempty"`
	}
	if err := a.call(ctx, "sendMessage", params, &result); err != nil {
		return err
	}
	if !result.Ok {
		return fmt.Errorf("telegram sendMessage: %s", result.Description)
	}
	return nil
}

func (a *HTTPAPI) call(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := "https://api.telegram.org/bot" + a.token + "/" + method
	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		// Re-read body for error context is awkward; report the decoder error.
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("telegram %s: empty body (HTTP %d)", method, resp.StatusCode)
		}
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	return nil
}
