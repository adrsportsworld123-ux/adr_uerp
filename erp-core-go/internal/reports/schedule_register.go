package reports

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/httpx"
)

// The Schedule H1 register (migrations/084, §3.87): what the Drugs Rules
// ask a chemist to record for each Schedule H1 (and narcotic) sale — the
// date, the drug and quantity supplied, its batch, the patient and the
// prescriber, with their addresses — from the sales themselves, by the
// merchant's calendar day. As a list, a CSV and an A4 PDF to keep or hand
// to an inspector. It is the register, not a filing: no drug-control
// authority publishes an API to file it to.

type registerLine struct {
	Date           string  `json:"date"`
	OrderID        string  `json:"order_id"`
	OrderNumber    string  `json:"order_number"`
	ProductName    string  `json:"product_name"`
	SKU            string  `json:"sku"`
	DrugSchedule   string  `json:"drug_schedule"`
	Quantity       string  `json:"quantity"`
	Batches        string  `json:"batches"` // "B123 (2), B124 (1)"
	PatientName    *string `json:"patient_name"`
	PatientPhone   *string `json:"patient_phone"`
	PatientAddress *string `json:"patient_address"`
	DoctorName     *string `json:"doctor_name"`
	DoctorRegNo    *string `json:"doctor_reg_no"`
	DoctorAddress  *string `json:"doctor_address"`
	PrescriptionID *string `json:"prescription_id"`
	// Missing: what the register needs and this sale lacks.
	Missing []string `json:"missing"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// GetScheduleRegister: GET /reports/schedule-register?branch_id=&from=&to=
// &schedule=&format=json|csv|pdf (audit.view). schedule "" = every
// schedule but OTC; "H1" etc. narrows it. from/to default to today; a PDF
// needs a branch (its letterhead).
func (h *Handler) GetScheduleRegister(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	branchID, schedule, format := q.Get("branch_id"), strings.TrimSpace(q.Get("schedule")), q.Get("format")
	from, to := q.Get("from"), q.Get("to")
	for _, d := range []*string{&from, &to} {
		if *d == "" {
			continue
		}
		if !IsValidDate(*d) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from/to must be YYYY-MM-DD")
			return
		}
	}
	if format != "" && format != "json" && format != "csv" && format != "pdf" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "format must be json, csv or pdf")
		return
	}
	if format == "pdf" && branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "choose a branch for the PDF")
		return
	}

	lines := []registerLine{}
	var lh documents.Letterhead
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if from == "" || to == "" {
			var today string
			if err := tx.QueryRow(ctx, `SELECT (now() AT TIME ZONE app_merchant_tz())::date::text`).Scan(&today); err != nil {
				return err
			}
			if from == "" {
				from = today
			}
			if to == "" {
				to = today
			}
		}
		rows, err := tx.Query(ctx, `
			SELECT (so.finalized_at AT TIME ZONE app_merchant_tz())::date::text, so.id::text, so.order_number, p.name, pv.sku,
			       pv.attribute_combo->>'Drug Schedule', sol.quantity::text,
			       COALESCE((SELECT string_agg(sb.batch_no || ' (' || trim(trailing '.' FROM trim(trailing '0' FROM lb.quantity::text)) || ')', ', ' ORDER BY sb.batch_no)
			                 FROM sales_order_line_batches lb JOIN stock_batches sb ON sb.id = lb.batch_id
			                 WHERE lb.sales_order_line_id = sol.id), ''),
			       c.name, c.phone, c.address, rx.doctor_name, rx.doctor_reg_no, rx.doctor_address, so.prescription_id::text
			FROM sales_order_lines sol
			JOIN sales_orders so ON so.id = sol.sales_order_id
			JOIN product_variants pv ON pv.id = sol.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN customers c ON c.id = so.customer_id
			LEFT JOIN prescriptions rx ON rx.id = so.prescription_id
			WHERE so.status IN ('finalized', 'partially_refunded', 'refunded')
			  AND (so.finalized_at AT TIME ZONE app_merchant_tz())::date BETWEEN $1::date AND $2::date
			  AND ($3 = '' OR so.branch_id::text = $3)
			  AND COALESCE(pv.attribute_combo->>'Drug Schedule', 'OTC') <> 'OTC'
			  AND ($4 = '' OR lower(pv.attribute_combo->>'Drug Schedule') = lower($4) OR lower(pv.attribute_combo->>'Drug Schedule') = lower('Schedule ' || $4))
			ORDER BY so.finalized_at, so.order_number, p.name`, from, to, branchID, schedule)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l registerLine
			if err := rows.Scan(&l.Date, &l.OrderID, &l.OrderNumber, &l.ProductName, &l.SKU, &l.DrugSchedule, &l.Quantity, &l.Batches,
				&l.PatientName, &l.PatientPhone, &l.PatientAddress, &l.DoctorName, &l.DoctorRegNo, &l.DoctorAddress, &l.PrescriptionID); err != nil {
				return err
			}
			l.Missing = []string{}
			if l.PrescriptionID == nil {
				l.Missing = append(l.Missing, "prescription")
			} else {
				if strings.TrimSpace(str(l.DoctorAddress)) == "" {
					l.Missing = append(l.Missing, "prescriber's address")
				}
				if strings.TrimSpace(str(l.DoctorRegNo)) == "" {
					l.Missing = append(l.Missing, "prescriber's registration no.")
				}
			}
			if strings.TrimSpace(str(l.PatientAddress)) == "" {
				l.Missing = append(l.Missing, "patient's address")
			}
			lines = append(lines, l)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if format == "pdf" {
			rows.Close()
			var err error
			lh, _, err = documents.LoadLetterhead(ctx, tx, branchID, documents.KindInternal)
			return err
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the schedule register")
		return
	}

	switch format {
	case "csv":
		var buf bytes.Buffer
		buf.WriteString("\xef\xbb\xbf") // a UTF-8 BOM, so Excel reads it as UTF-8
		cw := csv.NewWriter(&buf)
		_ = cw.Write([]string{"Date", "Bill no.", "Drug", "SKU", "Schedule", "Quantity", "Batch", "Patient", "Patient phone", "Patient address",
			"Prescriber", "Registration no.", "Prescriber address", "Missing"})
		for _, l := range lines {
			_ = cw.Write([]string{l.Date, l.OrderNumber, l.ProductName, l.SKU, l.DrugSchedule, l.Quantity, l.Batches, str(l.PatientName), str(l.PatientPhone),
				str(l.PatientAddress), str(l.DoctorName), str(l.DoctorRegNo), str(l.DoctorAddress), strings.Join(l.Missing, "; ")})
		}
		cw.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="schedule-register-`+from+`-to-`+to+`.csv"`)
		_, _ = w.Write(buf.Bytes())
	case "pdf":
		d := scheduleRegisterDocument(lines, from, to, schedule)
		pdf, err := documents.Render(lh, d, documents.FormatA4)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render the schedule register")
			return
		}
		documents.WritePDF(w, "schedule-register-"+from+"-to-"+to+".pdf", pdf)
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "branch_id": branchID, "schedule": schedule, "lines": lines})
	}
}

func scheduleRegisterDocument(lines []registerLine, from, to, schedule string) documents.Document {
	title := "SCHEDULE DRUG REGISTER"
	if schedule != "" {
		title = "SCHEDULE " + strings.ToUpper(strings.TrimPrefix(strings.TrimPrefix(schedule, "Schedule "), "schedule ")) + " REGISTER"
	}
	period := from
	if to != from {
		period = from + " to " + to
	}
	d := documents.Document{
		Title: title,
		Date:  period,
		Meta:  [][2]string{{"Entries", strconv.Itoa(len(lines))}, {"Printed", time.Now().Format("02 Jan 2006 15:04")}},
		Columns: []documents.Column{
			{Title: "Date / bill", Width: 2.1}, {Title: "Drug · batch", Width: 3.2}, {Title: "Qty", Width: 0.9, Right: true},
			{Title: "Patient", Width: 3.2}, {Title: "Prescriber", Width: 3.2},
		},
	}
	missing := 0
	for _, l := range lines {
		drug := l.ProductName + " (" + l.DrugSchedule + ")"
		if l.Batches != "" {
			drug += "\nBatch " + l.Batches
		}
		patient := strings.TrimSpace(strings.Join(nonEmpty(str(l.PatientName), str(l.PatientPhone), str(l.PatientAddress)), "\n"))
		doctor := strings.TrimSpace(strings.Join(nonEmpty(str(l.DoctorName), prefix("Reg. ", str(l.DoctorRegNo)), str(l.DoctorAddress)), "\n"))
		if doctor == "" {
			doctor = "— no prescription —"
		}
		if len(l.Missing) > 0 {
			missing++
		}
		d.Rows = append(d.Rows, documents.Row{Cells: []string{l.Date + "\n" + l.OrderNumber, drug, documents.Qty(l.Quantity), patient, doctor}})
	}
	if len(lines) == 0 {
		d.Notes = append(d.Notes, "No scheduled-drug sales in this period.")
	}
	if missing > 0 {
		d.Notes = append(d.Notes, strconv.Itoa(missing)+map[bool]string{true: " entry lacks", false: " entries lack"}[missing == 1]+
			" something the register needs (a prescription, or a patient's or prescriber's address). Complete them on the customer and prescription records.")
	}
	d.Notes = append(d.Notes, "Pharmacist's signature: ______________________")
	return d
}

func nonEmpty(vs ...string) []string {
	out := []string{}
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func prefix(p, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return p + v
}
