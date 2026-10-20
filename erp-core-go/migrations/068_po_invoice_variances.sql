-- =====================================================================
-- Purchase invoice import against a purchase order, with the differences
-- a real delivery has (phase0_1_design.md §3.74).
--
-- goods_receipt_notes.po_variance_accepted: a GRN against a PO whose
-- receiver accepted that it differs from the PO, i.e. items that aren't on
-- the PO (a GRN line with no po_line_id) and more than was still pending
-- on a PO line. Only the invoice import sets it, and only when the user
-- ticks "accept the differences". Every other GRN keeps the existing rule:
-- only the PO's items, never past what is pending.
--
-- purchase_order_lines.excess_qty: what arrived beyond the ordered
-- quantity on such a GRN. received_qty still stops at the ordered quantity
-- (its CHECK is unchanged), so pending and the PO's status mean what they
-- always meant.
-- =====================================================================

ALTER TABLE goods_receipt_notes ADD COLUMN po_variance_accepted BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE purchase_order_lines ADD COLUMN excess_qty NUMERIC(14,3) NOT NULL DEFAULT 0 CHECK (excess_qty >= 0);
