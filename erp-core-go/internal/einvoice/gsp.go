// Package einvoice implements Phase 4's last sub-area: e-invoicing (IRN
// generation, QR code) and e-way bill generation
// (phased_roadmap.md; pos_frd_complete.md's Tax & GST section).
//
// Real IRN/e-way-bill generation requires a licensed GSP (GST Suvidha
// Provider) — ClearTax, Cygnet, Vayana, MasterGST, and a handful of others
// each offer this over their own REST API with their own auth scheme and
// onboarding process, and choosing one is a vendor decision only the
// merchant/operator can make (see migrations/022_einvoice.sql's header
// comment for the full reasoning). GSPClient is the seam that decision
// plugs into: every handler in this package talks to a GSP purely through
// this interface, never a concrete vendor's SDK, so wiring in a real
// provider later (cmd/api/main.go) never touches handlers.go, the schema,
// or any caller of this package.
package einvoice

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// IRNLineItem is the minimal per-line data a real GSP's IRN-generation
// call needs (HSN, taxable value, tax split) — deliberately not the full
// NIC e-invoice schema (which also wants UQC, discount, assessable value
// breakdowns, etc.) since the StubGSPClient never actually validates or
// transmits this payload; a real GSPClient implementation would extend
// this or accept a richer request type as part of that integration work.
type IRNLineItem struct {
	HSNCode      string
	Description  string
	Quantity     float64
	TaxableValue float64
	CGSTRate     float64
	SGSTRate     float64
	IGSTRate     float64
}

type IRNRequest struct {
	SalesOrderID    string
	SupplierGSTIN   string
	BuyerGSTIN      string
	BuyerLegalName  string
	InvoiceNumber   string
	InvoiceDate     time.Time
	TotalInvoiceVal float64
	Items           []IRNLineItem
}

type IRNResponse struct {
	IRN           string
	AckNo         string
	AckDate       time.Time
	SignedInvoice string
	SignedQRCode  string
}

type EWayBillRequest struct {
	SalesOrderID  string
	IRN           string // real NIC e-way bill generation is normally IRN-linked when an e-invoice already exists; empty is valid (e-way bill without e-invoice, e.g. a non-B2B interstate movement)
	SupplierGSTIN string
	BuyerGSTIN    string
	FromStateCode string
	ToStateCode   string
	DocumentValue float64
	VehicleNo     string
	TransporterID string
	DistanceKM    int
}

type EWayBillResponse struct {
	EWBNo      string
	EWBDate    time.Time
	ValidUntil time.Time
}

// GSPClient is the vendor-agnostic seam described in this file's package
// comment. CancelIRN/CancelEWayBill take the provider's own document
// number (the IRN or e-way-bill number), not this system's internal id.
type GSPClient interface {
	GenerateIRN(ctx context.Context, req IRNRequest) (IRNResponse, error)
	CancelIRN(ctx context.Context, irn, reason string) error
	GenerateEWayBill(ctx context.Context, req EWayBillRequest) (EWayBillResponse, error)
	CancelEWayBill(ctx context.Context, ewbNo, reason string) error
}

// ProviderName identifies which GSPClient implementation is in use — every
// e_invoices/e_way_bills row records this (gsp_provider) so a later switch
// of vendor is visible in the data, not just in code.
type ProviderName string

const ProviderStub ProviderName = "stub"

// StubGSPClient never makes a network call — it deterministically fabricates
// well-formed IRNs, QR payloads, and e-way-bill numbers so every other
// layer (handlers, the audit trail, erp-web-admin's UI, e2e tests) can be
// built and verified against realistic shapes before a real GSP contract
// exists. Nothing it returns is a genuine government-issued document
// number; anything built against this must be re-verified against a real
// GSP's sandbox before going anywhere near production e-invoicing.
type StubGSPClient struct{}

func NewStubGSPClient() *StubGSPClient { return &StubGSPClient{} }

func (s *StubGSPClient) Name() ProviderName { return ProviderStub }

// GenerateIRN fabricates a 64-hex-char IRN (matching the real NIC spec's
// IRN length — a SHA-256 hex digest) deterministically from the order id
// and invoice number, so calling it twice for the same request is at
// least reproducible even though nothing here is cryptographically
// meaningful the way a real IRP-signed hash is.
func (s *StubGSPClient) GenerateIRN(ctx context.Context, req IRNRequest) (IRNResponse, error) {
	sum := sha256.Sum256([]byte(req.SupplierGSTIN + "|" + req.InvoiceNumber + "|" + req.SalesOrderID))
	irn := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	qrPayload := fmt.Sprintf(`{"Irn":"%s","SellerGstin":"%s","BuyerGstin":"%s","InvNo":"%s","InvVal":%.2f}`,
		irn, req.SupplierGSTIN, req.BuyerGSTIN, req.InvoiceNumber, req.TotalInvoiceVal)
	return IRNResponse{
		IRN:           irn,
		AckNo:         "STUB" + irn[:12],
		AckDate:       now,
		SignedInvoice: fmt.Sprintf(`{"stub":true,"irn":"%s","note":"not a real IRP-signed payload"}`, irn),
		SignedQRCode:  base64.StdEncoding.EncodeToString([]byte(qrPayload)),
	}, nil
}

func (s *StubGSPClient) CancelIRN(ctx context.Context, irn, reason string) error {
	return nil
}

// GenerateEWayBill fabricates a 12-digit e-way-bill number (matching the
// real EWB number's length) and sets validity per the actual NIC rule of
// thumb this codebase can encode honestly: 1 day of validity per 200km
// (or part thereof), minimum 1 day — the real rule has additional
// distance/vehicle-type nuances a genuine GSP integration would need to
// match exactly.
func (s *StubGSPClient) GenerateEWayBill(ctx context.Context, req EWayBillRequest) (EWayBillResponse, error) {
	sum := sha256.Sum256([]byte(req.SalesOrderID + "|" + req.SupplierGSTIN + "|" + req.BuyerGSTIN))
	digits := make([]byte, 0, 12)
	for _, b := range sum {
		if len(digits) == 12 {
			break
		}
		digits = append(digits, '0'+(b%10))
	}
	now := time.Now().UTC()
	validDays := req.DistanceKM/200 + 1
	return EWayBillResponse{
		EWBNo:      string(digits),
		EWBDate:    now,
		ValidUntil: now.AddDate(0, 0, validDays),
	}, nil
}

func (s *StubGSPClient) CancelEWayBill(ctx context.Context, ewbNo, reason string) error {
	return nil
}
