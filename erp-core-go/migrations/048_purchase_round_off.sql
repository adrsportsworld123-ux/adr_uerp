-- =====================================================================
-- Supplier invoice round-off. A supplier's invoice total is often
-- rounded to the rupee (and its line rates to the paisa), so a purchase
-- bill entered from it needs a few paise either way to equal the printed
-- total without misstating GST input. POST /purchase/bills takes
-- round_off (under ₹1 either way) and posts the difference here.
-- Expense account: a debit balance is a loss, a credit balance a gain.
-- =====================================================================
INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
SELECT DISTINCT c.merchant_id, '5005', 'Round Off', 'expense', true
FROM chart_of_accounts c
WHERE c.code = '1001'
ON CONFLICT (merchant_id, code) DO NOTHING;
