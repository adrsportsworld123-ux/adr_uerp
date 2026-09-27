-- Distinguishes a variant's real, original barcode (whatever's actually
-- printed on the product — supplier/manufacturer-assigned, or a real code
-- staff typed in by hand) from one this system auto-generated in GS1's
-- reserved in-store-use range. Both can coexist per variant (the schema
-- already allowed multiple barcodes per variant; this just makes which is
-- which an explicit, queryable fact instead of an inferred convention).
ALTER TABLE barcodes ADD COLUMN source TEXT NOT NULL DEFAULT 'original' CHECK (source IN ('original','generated'));

-- Backfill existing rows using the same convention AssignBarcode's own
-- generation logic already relies on (EAN13 + '20' prefix = auto-generated).
UPDATE barcodes SET source = 'generated' WHERE symbology = 'EAN13' AND code LIKE '20%';
