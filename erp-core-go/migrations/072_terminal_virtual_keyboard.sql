-- =====================================================================
-- On-screen keyboard per POS terminal (phase0_1_design.md §3.77).
--
-- pos_terminals.virtual_keyboard: the till shows its own on-screen
-- keyboard whenever a text field is focused, for touch-screen POS
-- machines with no physical keyboard (and no system one, as on a Windows
-- touch PC). false (the default, every existing till) = as before. Sent to
-- the till with its other screen settings
-- (GET /settings/localization/terminal).
-- =====================================================================

ALTER TABLE pos_terminals ADD COLUMN virtual_keyboard BOOLEAN NOT NULL DEFAULT false;
