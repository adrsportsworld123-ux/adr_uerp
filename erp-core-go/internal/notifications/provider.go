// Package notifications implements Phase 3's Notifications (expanded)
// sub-area (phased_roadmap.md; tech_stack_decision.md §3.1's "Notification
// dispatch at volume (fan-out to Email/SMS/WhatsApp/Push providers)").
//
// This package is deliberately content-agnostic: it knows how to deliver
// a message and log the attempt, not what a receipt or a low-stock alert
// should say — that stays with whichever package owns the domain data
// (internal/sales formats receipts, internal/inventory formats low-stock
// bodies, internal/purchase formats bill reminders), each calling
// Handler.DispatchEmail/DispatchPhone. This keeps the dependency direction
// one-way (sales/inventory/purchase -> notifications) with no risk of the
// cycle internal/loyalty had to solve differently (see
// internal/sales/handlers.go's LoyaltyEarner doc comment).
//
// Real vendor SMS/WhatsApp integration (Twilio, MSG91, Gupshup, ...) needs
// a chosen provider and real credentials, neither of which exist in this
// solo-builder sandbox — ConsoleProvider is the same "real interface,
// dev-only stand-in for the piece needing credentials/hardware this
// environment doesn't have" pattern DEV_AUTH_TOOLS_ENABLED and
// internal/printing's ESC/POS driver already established. Email is
// different: SMTP is a standard, vendor-neutral protocol Go's stdlib
// speaks directly, so SMTPProvider is a real, working integration against
// any SMTP server/relay you point it at.
package notifications

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
)

type Channel string

const (
	ChannelEmail    Channel = "email"
	ChannelSMS      Channel = "sms"
	ChannelWhatsApp Channel = "whatsapp"
)

type Message struct {
	Channel   Channel
	Recipient string
	Subject   string // email only
	Body      string
}

type Provider interface {
	Send(ctx context.Context, msg Message) error
}

// ConsoleProvider logs the message it would have sent instead of actually
// sending it — the dev-only stand-in for SMS/WhatsApp (no vendor chosen)
// and for email when SMTP isn't configured. Always succeeds: a checkout
// or a sweeper tick must never fail because the notification layer has
// nothing real to send through.
type ConsoleProvider struct{}

func (ConsoleProvider) Send(_ context.Context, msg Message) error {
	log.Printf("[notifications:console] channel=%s to=%s subject=%q body=%q", msg.Channel, msg.Recipient, msg.Subject, msg.Body)
	return nil
}

// SMTPConfig holds a standard SMTP server's connection details — works
// against Gmail (with an app password), SendGrid/Mailgun/Postmark's SMTP
// relay, or any self-hosted MTA, since none of this is vendor-specific.
type SMTPConfig struct {
	Host, Port, Username, Password, From string
}

type SMTPProvider struct {
	cfg SMTPConfig
}

func NewSMTPProvider(cfg SMTPConfig) *SMTPProvider {
	return &SMTPProvider{cfg: cfg}
}

func (p *SMTPProvider) Send(_ context.Context, msg Message) error {
	if msg.Channel != ChannelEmail {
		return fmt.Errorf("SMTPProvider only sends email, got channel %q", msg.Channel)
	}
	addr := p.cfg.Host + ":" + p.cfg.Port
	var auth smtp.Auth
	if p.cfg.Username != "" {
		auth = smtp.PlainAuth("", p.cfg.Username, p.cfg.Password, p.cfg.Host)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"utf-8\"\r\n\r\n",
		p.cfg.From, msg.Recipient, msg.Subject)
	return smtp.SendMail(addr, auth, p.cfg.From, []string{msg.Recipient}, []byte(headers+msg.Body))
}

// routingProvider sends email through whichever provider NewProviderFromConfig
// resolved (SMTP if configured, console otherwise) and always sends
// sms/whatsapp through console — there is no real phone-channel vendor
// integration to route to yet (see package doc comment).
type routingProvider struct {
	email Provider
	phone Provider
}

func (r routingProvider) Send(ctx context.Context, msg Message) error {
	if msg.Channel == ChannelEmail {
		return r.email.Send(ctx, msg)
	}
	return r.phone.Send(ctx, msg)
}

// NewProviderFromConfig builds the Provider main.go wires into Handler.
// smtpHost empty means "SMTP isn't configured" — degrades to
// ConsoleProvider for email too, the same "missing config disables the
// feature gracefully rather than failing startup" pattern
// internal/search.Client already established for OPENSEARCH_URL.
func NewProviderFromConfig(smtpHost string, cfg SMTPConfig) Provider {
	var email Provider = ConsoleProvider{}
	if smtpHost != "" {
		email = NewSMTPProvider(cfg)
	}
	return routingProvider{email: email, phone: ConsoleProvider{}}
}
