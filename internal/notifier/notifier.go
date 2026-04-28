// Package notifier delivers events to the operator over Discord and accepts
// inbound DM commands.
package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ishankkm/siren/internal/event"
	"github.com/ishankkm/siren/internal/redact"
)

// CommandHandler receives inbound operator messages and returns a reply.
// An empty reply means "no response".
type CommandHandler func(body string) string

// Discord is the Discord-backed notifier.
type Discord struct {
	session    *discordgo.Session
	operatorID string
	channelID  string
	logger     *slog.Logger
	redactor   *redact.Redactor

	queue   *queue
	handler CommandHandler
}

// Options configure a new Discord notifier.
type Options struct {
	Token      string
	OperatorID string
	QueueSize  int
	Redactor   *redact.Redactor
	Logger     *slog.Logger
	Handler    CommandHandler
}

// New constructs a Discord notifier. The session is not yet opened.
func New(opt Options) (*Discord, error) {
	if opt.Token == "" {
		return nil, errors.New("discord token is empty")
	}
	if opt.OperatorID == "" {
		return nil, errors.New("operator id is empty")
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.QueueSize <= 0 {
		opt.QueueSize = 256
	}
	s, err := discordgo.New("Bot " + opt.Token)
	if err != nil {
		return nil, fmt.Errorf("discordgo.New: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	d := &Discord{
		session:    s,
		operatorID: opt.OperatorID,
		logger:     opt.Logger.With("component", "notifier"),
		redactor:   opt.Redactor,
		queue:      newQueue(opt.QueueSize),
		handler:    opt.Handler,
	}
	s.AddHandler(d.onMessage)
	return d, nil
}

// Notify enqueues e for delivery. Drops the oldest event if the queue is full.
func (d *Discord) Notify(e event.Event) {
	if dropped := d.queue.push(e); dropped {
		d.logger.Warn("outbound queue full; dropped oldest event")
	}
}

// Run opens the gateway, sends a startup DM, drains the queue, and on ctx
// cancellation sends a best-effort shutdown DM and closes.
func (d *Discord) Run(ctx context.Context) error {
	if err := d.session.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}
	defer func() {
		_ = d.session.Close()
	}()

	ch, err := d.session.UserChannelCreate(d.operatorID)
	if err != nil {
		return fmt.Errorf("create DM channel: %w", err)
	}
	d.channelID = ch.ID

	d.sendPlain(ctx, "siren up")

	go d.drain(ctx)

	<-ctx.Done()

	// Best-effort shutdown DM with a short detached deadline.
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d.sendPlain(shutCtx, "siren shutting down")
	return nil
}

func (d *Discord) drain(ctx context.Context) {
	backoff := time.Second
	for {
		ev, ok := d.queue.pop(ctx)
		if !ok {
			return
		}
		for {
			err := d.send(ctx, ev)
			if err == nil {
				backoff = time.Second
				break
			}
			d.logger.Warn("send failed; will retry", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
	}
}

func (d *Discord) send(ctx context.Context, e event.Event) error {
	embed := d.format(e)
	_, err := d.session.ChannelMessageSendEmbed(d.channelID, embed, discordgo.WithContext(ctx))
	return err
}

func (d *Discord) sendPlain(ctx context.Context, msg string) {
	if d.channelID == "" {
		return
	}
	if _, err := d.session.ChannelMessageSend(d.channelID, msg, discordgo.WithContext(ctx)); err != nil {
		d.logger.Warn("plain send failed", "err", err)
	}
}

// Reply sends a free-form text response (used by the command handler).
func (d *Discord) Reply(ctx context.Context, msg string) {
	d.sendPlain(ctx, msg)
}

// Discord embed limits. See https://discord.com/developers/docs/resources/channel#embed-object-embed-limits
const (
	maxEmbedDescription = 4096
	maxEmbedFieldValue  = 1024
	// codeFenceOverhead is the length of the surrounding "```\n" and "\n```"
	// added to the detail field value.
	codeFenceOverhead = len("```\n") + len("\n```")
	truncMarker       = "\n…(truncated)"
)

func (d *Discord) format(e event.Event) *discordgo.MessageEmbed {
	summary := e.Summary
	detail := e.Detail
	if d.redactor != nil {
		summary = d.redactor.Apply(summary)
		detail = d.redactor.Apply(detail)
	}
	summary = truncate(summary, maxEmbedDescription)
	// detail is wrapped in a code fence in the field value, so the body
	// itself must be <= maxEmbedFieldValue - codeFenceOverhead.
	detail = truncate(detail, maxEmbedFieldValue-codeFenceOverhead)
	color := severityColor(e.Severity)
	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("[%s] %s", e.Service, e.Severity),
		Description: summary,
		Color:       color,
		Timestamp:   e.Timestamp.Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("source=%s · fp=%s", e.Source, e.Fingerprint),
		},
	}
	if detail != "" && detail != summary {
		embed.Fields = []*discordgo.MessageEmbedField{{
			Name:  "detail",
			Value: "```\n" + detail + "\n```",
		}}
	}
	return embed
}

// truncate returns s shortened to at most limit bytes, appending a truncation
// marker when shortening occurs. The returned string is always <= limit bytes.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= len(truncMarker) {
		return s[:limit]
	}
	return s[:limit-len(truncMarker)] + truncMarker
}

func severityColor(s event.Severity) int {
	switch s {
	case event.SeverityInfo:
		return 0x2ecc71 // green
	case event.SeverityWarn:
		return 0xf1c40f // yellow
	case event.SeverityError:
		return 0xe67e22 // orange
	case event.SeverityCritical:
		return 0xe74c3c // red
	default:
		return 0x95a5a6
	}
}

func (d *Discord) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}
	if m.Author.ID != d.operatorID {
		return
	}
	if m.GuildID != "" {
		return // operator must DM siren, not post in a guild channel
	}
	if d.handler == nil {
		return
	}
	reply := d.handler(m.Content)
	if reply == "" {
		return
	}
	if _, err := s.ChannelMessageSend(m.ChannelID, reply); err != nil {
		d.logger.Warn("reply failed", "err", err)
	}
}

// queue is a bounded drop-oldest queue of events.
type queue struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []event.Event
	cap  int
}

func newQueue(capacity int) *queue {
	q := &queue{cap: capacity}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) push(e event.Event) (dropped bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.buf) >= q.cap {
		q.buf = q.buf[1:]
		dropped = true
	}
	q.buf = append(q.buf, e)
	q.cond.Signal()
	return dropped
}

func (q *queue) pop(ctx context.Context) (event.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	stop := context.AfterFunc(ctx, func() {
		q.mu.Lock()
		q.cond.Broadcast()
		q.mu.Unlock()
	})
	defer stop()

	for len(q.buf) == 0 {
		if ctx.Err() != nil {
			return event.Event{}, false
		}
		q.cond.Wait()
	}
	e := q.buf[0]
	q.buf = q.buf[1:]
	return e, true
}

// Depth returns the current queue depth (used by !status).
func (d *Discord) Depth() int {
	d.queue.mu.Lock()
	defer d.queue.mu.Unlock()
	return len(d.queue.buf)
}
