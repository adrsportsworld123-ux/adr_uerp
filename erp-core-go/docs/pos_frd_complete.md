# Universal POS System - Complete Functional Requirements Document

**Version:** 1.0 | **Date:** December 21, 2025 | **Status:** Final

---

## 📥 How to Download This Document

**To save this complete FRD:**
1. Click the menu (⋮) at top-right of this window
2. Select "Download" or copy all content
3. Save as `.md` file or paste into Word/Google Docs
4. Convert to PDF using Pandoc, Typora, or Word's export feature

**Document Size:** ~50 pages | **25 Functional Modules** | **Complete Specification**

---

## 📋 Table of Contents

**PART A: PROJECT OVERVIEW**
- [Executive Summary](#executive-summary)
- [System Overview](#system-overview)
- [Target Market & Users](#target-market)

**PART B: CORE FUNCTIONAL MODULES (1-10)**
1. [Multi-Tenancy Architecture](#1-multi-tenancy-architecture)
2. [Inventory Management](#2-inventory-management)
3. [Sales & Billing](#3-sales--billing)
4. [Tax & GST Compliance](#4-tax--gst-compliance)
5. [Promotions & Loyalty](#5-promotions--loyalty)
6. [Customer Management](#6-customer-management)
7. [Employee & Payroll](#7-employee--payroll)
8. [Ledger & Accounting](#8-ledger--accounting)
9. [Purchase Management](#9-purchase-management)
10. [Product & Catalog Management](#10-product--catalog-management)

**PART C: PRODUCT & PRICING MODULES (11-16)**
11. [Pricing Management](#11-pricing-management)
12. [Barcode & Label Generation](#12-barcode--label-generation)
13. [Product Search & Filtering](#13-product-search--filtering)
14. [Product Content Management](#14-product-content-management)

**PART D: BUSINESS INTELLIGENCE (15-18)**
15. [Reports & Analytics](#15-reports--analytics)
16. [Reconciliation & Audit](#16-reconciliation--audit)
17. [Notifications & Alerts](#17-notifications--alerts)

**PART E: SECURITY & SUBSCRIPTION (18-21)**
18. [Authentication & Authorization](#18-authentication--authorization)
19. [Subscription & Billing](#19-subscription--billing)

**PART F: TECHNICAL ARCHITECTURE (20-25)**
20. [Technology Stack](#20-technology-stack)
21. [Data Synchronization](#21-data-synchronization)
22. [Performance Requirements](#22-performance-requirements)
23. [User Experience](#23-user-experience)
24. [Testing & QA Strategy](#24-testing--qa-strategy)
25. [Future Enhancements](#25-future-enhancements)

**APPENDICES**
- [Data Models](#appendix-a-key-data-models)
- [API Endpoints](#appendix-b-api-structure)
- [Implementation Roadmap](#appendix-c-implementation-roadmap)
- [Glossary](#appendix-d-glossary)

---

# PART A: PROJECT OVERVIEW

## Executive Summary

### Project Vision
Universal POS System is a comprehensive, cloud-based retail management solution serving single-store retailers through multi-location franchises across all retail verticals, with special focus on sports shops and neighborhood retail stores.

### Key Objectives
✅ Multi-tenant SaaS (Merchant → Branch → POS hierarchy)  
✅ Offline-first POS with intelligent sync  
✅ Complete retail suite (Inventory, Sales, Purchase, Accounting, HR, CRM)  
✅ Indian GST compliance (e-invoicing, e-way bill, GSTR filing)  
✅ Tiered subscription (₹999-₹3,999/POS/month + Freemium)  
✅ Peak capacity: 10,000 transactions/hour  

### Target Market
**Primary Verticals:** Sports Equipment, Grocery, Fashion, Electronics, F&B  
**Scale:** 1 store to unlimited branches/franchises  
**Geography:** India-focused, globally capable (multi-currency)  
**Segments:** B2C Retail + B2B Wholesale  

### Performance Targets
- Transaction completion: ≤6 seconds (scan to receipt)
- Product search: ≤100ms
- Dashboard refresh: ≤3 seconds
- Uptime SLA: 99.9%

---

# PART B: CORE FUNCTIONAL MODULES

## 1. Multi-Tenancy Architecture

### Hierarchy
```
Merchant (Tenant)
  └── Branch (Multiple)
        └── POS Terminal (Multiple per Branch)
```

### Data Isolation
**Hybrid Model Supported:**
- Option A: Separate database per merchant (complete isolation)
- Option B: Shared database with row-level tenant filtering
- Configurable per deployment

### Access Control
- **Merchant Admin:** All branches, all data, system configuration
- **Branch Manager:** Single branch data, branch operations, local reports
- **POS User:** Assigned terminal, transaction processing only
- **Custom Roles:** Dynamic role creation at merchant/branch level

### Cross-Branch Capabilities
✅ Consolidated reporting across all branches (real-time)  
✅ Inventory transfers with approval workflow  
✅ Unified customer data (loyalty, credit, purchase history)  
✅ Centralized product catalog with branch-specific pricing/stock  

---

## 2. Inventory Management

### Tracking Levels (All Supported)
- **SKU-level:** Basic stock units
- **Variant-level:** Size × Color × Style combinations
- **Serial Number:** Individual high-value items (bikes, electronics)
- **Batch/Lot:** Expiry tracking (medicines, perishables)

### Stock Reservation
- **Method:** Soft reservation with optimistic locking
- **Timeout:** 15 minutes for cart items
- **Release:** Auto-release on abandonment/cancellation
- **Benefit:** Prevents overselling while maintaining availability

### Inter-Branch Transfer
**Workflow:**
1. Branch A initiates → "Pending Approval"
2. Manager/Admin approves → "Approved"
3. Goods dispatched → "In-Transit" (stock deducted from A)
4. Goods received at B with GRN → "Completed" (stock added to B)

**Features:** In-transit tracking, rejection/cancellation, automated notifications

### Reorder Management
**Hybrid Approach (Recommended):**
- Auto-generate PO drafts when stock hits reorder point
- Include smart suggestions (sales velocity, seasonal trends, lead time)
- Require manager approval before sending to supplier
- Configurable reorder point/quantity per product/branch

### Valuation Methods
**Primary: Weighted Average Cost (WAC)**
- Auto-calculate on each purchase
- Formula: (Existing Value + New Value) / (Existing Qty + New Qty)
- Ideal for real-time POS

**Alternative: FIFO (First-In-First-Out)**
- Optional for perishables or compliance needs
- Track purchase batches separately

**Configuration:** Merchant-level setting

### Stock Adjustments
- Reasons: Physical count, damage, theft, samples, conversions
- Approval required for adjustments >threshold
- Auto-create journal entries
- Full audit trail (who, when, why, quantity, value)

---

## 3. Sales & Billing

### Payment Methods (Configurable Cascade)
**All Supported:** Cash, Credit/Debit Cards, UPI, Wallets, BNPL, Gift Cards, Cheque, Cryptocurrency, Split Payments

**Configuration Flow:** Merchant enables → Branch selects from enabled → POS inherits branch selection

### Return & Exchange
**Hybrid Model (Configurable):**
- **Option A:** Immediate stock update + refund
- **Option B:** Approval workflow → Manager reviews → Then process
- Return window configurable per category
- Track return reason
- Restocking fee option
- Exchange for same/different product

### Partial Payments / Layaway
- Customer pays in installments
- Stock reserved (not available for sale)
- Payment schedule with reminders
- Collect item after full payment
- Cancellation policy (refund or store credit)

### Bill Modification
**Hybrid (Configurable):**
- **Pre-finalization:** Direct edit before payment
- **Post-finalization:** Edit with manager approval + reason
- **Audit Trail:** All changes logged (original→new, user, timestamp, reason)

### Transaction Flow
1. Scan/search product → Add to cart
2. Apply discounts/coupons/loyalty
3. Select customer (or walk-in)
4. Calculate tax (auto GST)
5. Select payment method(s)
6. Process payment
7. Print/email receipt
8. Update inventory, ledgers, cash register

**Performance:** ≤6 seconds total

---

## 4. Tax & GST Compliance

### GST Calculation
**Automatic:**
- **Intrastate:** CGST + SGST (e.g., 9% + 9%)
- **Interstate:** IGST (e.g., 18%)
- Based on merchant GSTIN location and customer address

### Tax Slabs
- Pre-configured: 0%, 5%, 12%, 18%, 28%
- Admin can add new slabs
- Assign per product (mandatory)
- Support for cess

### HSN/SAC Codes
- Mandatory at product level
- Optional government validation (merchant config)
- Auto-suggest based on category

### GST Returns
- **GSTR-1:** Monthly outward supplies report
- **GSTR-3B:** Monthly summary with tax payment
- Export JSON for GST portal upload
- Reconciliation tool

### E-Invoicing
- Generate e-invoices for B2B (>₹5 crores turnover)
- Real-time IRN from NIC portal
- QR code for verification
- Auto-retry on failures

### E-Way Bill
- Auto-generate for interstate >₹50,000
- Track validity (distance-based)
- Extend/cancel capability

### Reverse Charge Mechanism
- Support purchases from unregistered dealers
- Merchant pays GST on behalf of supplier
- Separate GSTR-3B reporting

### Tax Exemptions
- Embassies, exports, SEZ units
- Certificate upload and validation
- Tax-exempt invoice generation

---

## 5. Promotions & Loyalty

### Discount Hierarchy (Application Order)
1. Product/Category/Subcategory base discounts
2. Promotional discounts (BOGO, bundles, volume)
3. Coupon codes
4. Customer-level discounts (VIP/wholesale)
5. Loyalty points redemption
6. Manual discounts (with authorization)

**Stacking:** Configurable per promotion (Exclusive vs Stackable)

### Promotion Types (All)
- **Percentage/Fixed Off:** 10% off, Flat ₹500 off
- **BOGO:** Buy 2 Get 1, Buy 1 Get 50% off 2nd
- **Bundles:** Kit pricing (Bat+Ball+Gloves = ₹3,999)
- **Volume:** Buy 3-5: 5% off, 6-10: 10% off, 11+: 15% off
- **Minimum Value:** Spend ₹2,000 get ₹200 off
- **Time-based:** Happy hours (2-5 PM: 20% off), Flash sales

**Application Levels:** Product, Category, Subcategory, Customer

### Coupon Constraints
- Usage limits (per customer, total cap)
- Validity period (date/time)
- Minimum purchase value
- Payment method restrictions
- Channel restrictions (POS/Online/Mobile)
- Branch-specific

### Loyalty Points
**Earning:** Configurable (₹100 = 1 point OR 10 points/transaction)  
**Redemption:** Configurable (100 points = ₹10)  
**Expiry:** 1 year from last transaction (rolling)  
**Restriction:** Cannot earn and redeem in same transaction  

### Manual Discount Authorization
- **0-5%:** POS User (no approval)
- **5-15%:** Branch Manager (PIN override)
- **15-25%:** Merchant Admin (OTP)
- **Above 25%:** Owner approval + mandatory reason
- Full audit trail

---

## 6. Customer Management

### Customer Types
**B2C:** Walk-in allowed (optional registration)  
**B2B:** Mandatory registration, GSTIN required  

**Mandatory Fields:** Name, Phone, Email  
**Optional:** Address, DOB, Anniversary, Company, GSTIN, Payment Terms

### Segmentation (Auto)
- **VIP:** >₹1L purchases or >50 transactions
- **Regular:** 10-50 transactions
- **New:** <10 transactions
- **Dormant:** No purchase in 6 months

**Segment Actions:** Different pricing, targeted promotions, priority support

### Credit Facility (B2B)
- Credit limit per customer
- Credit period (Net 30/60/90)
- Real-time outstanding tracking
- Overdue aging (0-30, 30-60, 60-90, 90+)
- Auto-block on limit breach
- Payment reminders (pre-due and overdue)

### Multi-location
- Profile accessible at all branches
- Purchase history consolidated
- Loyalty points shared (earn at A, redeem at B)
- Credit limit unified

---

## 7. Employee & Payroll

### Employee Types
Full-time, Part-time, Contract, Commission-based, Daily Wage (each with different payroll rules)

### Attendance
- POS clock-in/clock-out
- Shift management
- Overtime calculation
- Late/early tracking
- Biometric/PIN authentication

### Payroll Components
**Earnings:** Basic, HRA, VDA, Allowances, Commission, Incentives  
**Deductions:** TDS, PF, ESI, PT, LWF, Loans, Advances  
**Net = Gross Earnings - Total Deductions**

### Commission (Recommended Model)
- **Net Sales After Returns** (prevents commission on returns)
- **Product Category Weighted** (Sports: 5%, Accessories: 3%, Apparel: 2%)
- **Tiered:** 0-₹50k: 2%, ₹50k-₹1L: 3%, >₹1L: 5%
- **Target Multiplier:** Achieve target → Apply 1.5x
- **Payment:** After return period (15-30 days)

### Payroll Frequencies
Monthly, Bi-weekly, Weekly, Daily (configurable per employee)

### Statutory Compliance
- **PF:** 12% employee + 12% employer (split: 8.33% EPF, 3.67% EPS)
- **ESI:** 0.75% employee + 3.25% employer (for salary ≤₹21k)
- **TDS:** Per slab with Form 16 generation
- **PT:** State-specific monthly deduction
- **LWF:** Annual contribution
- Auto-generate challans for all

---

## 8. Ledger & Accounting

### Chart of Accounts
**Default Categories:** Assets, Liabilities, Equity, Income, Expenses  
**Customization:** Add accounts, sub-accounts, multi-level hierarchy, import/export

### Double-Entry System
- Every transaction auto-creates debit/credit entries
- Manual journal entry with approval
- Complete audit trail

**Example - Cash Sale ₹1,000:**
```
Debit: Cash Account         ₹1,000
Credit: Sales Revenue       ₹1,000
```

### Party Ledgers
- **Customers:** Receivables tracking
- **Suppliers:** Payables tracking
- **Employees:** Salary payables
- **Banks/Cash:** All cash/bank transactions

### Payment Terms
**Predefined:** Net 7/15/30/60/90, COD, Advance, Partial  
**Custom:** Credit days, early payment discount (2/10 Net 30), late penalty  
**Aging:** Current, 0-30, 30-60, 60-90, 90+ days  
**Reminders:** 7 days before, on due date, 3 days after, weekly  
**Credit Hold:** Auto-block if overdue or limit exceeded

### Bank Reconciliation
**Hybrid:**
- Auto-import statements (CSV/Excel/PDF)
- Auto-match by amount, date, reference
- Manual matching for unmatched
- Reconciliation reports

### Multi-currency
- All major currencies
- Auto-fetch exchange rates (with manual override)
- Foreign exchange gain/loss calculation
- Multi-currency ledgers

---

## 9. Purchase Management

### PO Workflow (Hybrid - Configurable)
**Option A:** Direct entry (GRN → Bill)  
**Option B:** Formal workflow (PO → Approval → Send → GRN → Invoice → Payment)

### Partial Deliveries
- Track multiple GRNs against single PO
- Pending quantity monitoring
- Status: Partially Received → Fully Received

### Purchase Returns
- Auto-generate debit notes
- Immediate supplier ledger adjustment
- Immediate inventory reduction

### Supplier Management
**Fields:** Name, Contact, Email, GSTIN, Legal Name, Payment Terms, Credit Limit  
**Tracking:** Purchase history, performance (delivery time, quality), multiple contacts

### Purchase Pricing
- Last purchase price per supplier
- Price comparison across suppliers
- Landed cost (price + freight + taxes)

### GRN (Goods Receipt Note)
**Three-way Matching:** PO → GRN → Supplier Invoice  
Quality check during GRN  
Direct GRN (without PO) option

---

## 10. Product & Catalog Management

### Hierarchy
- **Multi-level:** Category → Subcategory → Sub-subcategory
- **Collections:** "Summer 2025", "Festive Sale"
- **Brands:** Manufacturer management

### Attributes
- Custom per category (Size, Color, Capacity, Weight, Material)
- Unit management (kg, liter, meter, cm)
- Technical specs, care instructions
- Multiple images (primary + gallery, optional)

### Variants
**Matrix:** Size × Color × Style = Auto-generate SKUs  
**Example:** T-shirt S/M/L × Red/Blue/Green = 9 SKUs  
**Per Variant:** Pricing, images (optional), inventory

### Composite Products
- Bundle as single SKU
- Deduct components on sale
- Example: "Cricket Kit" = Bat + Ball + Gloves + Bag

### Lifecycle
- **Status:** Active, Inactive, Discontinued, Out of Stock
- **Seasonal:** Auto-activation dates
- **Expiry:** Date tracking for perishables
- **Batch/Lot:** Number tracking

### Bulk Operations
- Upload via Excel/CSV with template
- Catalog export for backup

---

# PART C: PRODUCT & PRICING MODULES

## 11. Pricing Management

### Pricing Tiers
- Cost Price (margin calculation)
- MRP (mandatory in India)
- Base Selling Price
- Retail, Wholesale, VIP, Special (promo)

### Customer-specific Pricing
- Price tier per customer type
- Customer-level overrides
- Volume-based (buy 10+ get wholesale rate)

### Dynamic Pricing
- Time-based (happy hours)
- Day-specific (weekend specials)
- Seasonal with auto-activation

### Price Lists
- Multiple lists (Retail, Wholesale, Export, Online)
- Assign to segments, branches, channels
- Scheduled price changes (future effective date)
- Price history audit trail

### Margin & Markup
- Auto-calculate markup % and margin %
- Cost-plus pricing tool (cost + markup% = selling price)
- Target margin tool (cost + margin% = selling price)
- Minimum margin alerts
- Block negative margin sales (override required)

### Bulk Updates
- By category, brand, supplier, price range
- Methods: % change, fixed amount, set new price
- Rounding (₹1, ₹5, ₹10, ₹50, ₹100)
- Preview → Approval → Apply

### Multi-currency
- Price in multiple currencies per product
- Auto-convert OR manual entry per currency
- Exchange rate management

### Tax Display
- Tax-inclusive OR tax-exclusive (merchant config)
- Display both on POS (configurable)

---

## 12. Barcode & Label Generation

### Barcode Types
**Retail:** EAN-13 (primary), EAN-8, UPC-A, UPC-E  
**Alphanumeric:** Code 128, Code 39  
**2D:** QR Code  

### Generation
- **Auto:** Sequential with check digit
- **Manual:** Entry for supplier barcodes
- **Hybrid (Recommended):** Use supplier barcode if exists, else auto-generate
- Validation, duplicate prevention
- Multiple barcodes per product (aliases)

### Label Templates
**Sizes:** 40×20mm, 40×30mm, 50×30mm, 75×50mm, 100×50mm, custom  
**Content:** Product name, SKU, barcode, MRP, price, variant, batch/lot, expiry, logo, branch  
**Editor:** WYSIWYG drag-and-drop designer  

**Predefined Templates:**
1. Standard (50×30mm): Name, barcode, MRP, SKU
2. Compact (40×20mm): Name, barcode, MRP
3. Detailed (100×50mm): Full info with batch/expiry
4. QR Code (50×50mm): QR + name + price

### Printer Support
**Brands:** Zebra, TSC, Brother, Epson, DYMO  
**Connectivity:** USB, Bluetooth, Network, Wi-Fi  
**Protocols:** ESC/POS, ZPL, TSPL  

### Bulk Printing
- Multiple labels per product (qty specification)
- Print entire GRN labels
- Select by category/brand, specify qty each

### Weighing Scale Integration
- Embed weight in barcode (variable weight items)
- PLU codes (4-5 digits for price lookup)
- Scale types: USB, Serial, Bluetooth, Network
- Format: `2{PLU}{Weight}{Price}{Check}`

---

## 13. Product Search & Filtering

### Search (OpenSearch-powered)
**Fields:** Name, SKU, Barcode, HSN, Brand, Category, Supplier, Attributes, Description, Tags  
**Performance:** ≤100ms

### Autocomplete
- Type-ahead (≥2 chars)
- Response: ≤50ms
- Top 10 with thumbnails, price, stock

### Fuzzy Search
- Typo tolerance (edit distance 2)
- Phonetic matching
- "Did you mean?" suggestions

### Filters
**Multi-select:** Category, Brand, Supplier  
**Range:** Price (slider + presets)  
**Status:** In Stock, Low Stock, Out of Stock  
**Attributes:** Size, Color, custom  
**Other:** Discount %, Product status  

### Sort
Relevance, Name A-Z/Z-A, Price Low-High/High-Low, Newest, Stock, Best Selling, Margin

### Quick Access
- **Recently Viewed:** Last 20 per user
- **Frequently Sold:** Today/week, branch-specific
- **Favorites:** User-pinned products

### Views
- Grid (default)
- List (detailed)
- Compact (POS maximum density)

### Advanced
- Search operators (exact, exclude, OR, range)
- Barcode-first (if all numbers)
- Saved searches
- Search history

---

## 14. Product Content Management

### Descriptions
**Short:** 100-200 chars, plain text (POS/receipts)  
**Long:** Unlimited, rich text (Bold, Italic, Lists, Headings, Links, Tables, Images)  
**Editor:** WYSIWYG (TinyMCE/CKEditor)  
**Multi-language:** Default English + Hindi, Tamil, Arabic (auto-translate option)

### Tags & Keywords
- Product tags (descriptive, use case, season, trend, material)
- Auto-suggest, hierarchy
- Search keywords (hidden, for better matching)

### Product Relations
1. **Related:** "Customers also bought" (auto from transactions)
2. **Cross-sell:** "Frequently bought together" (bundle suggestions)
3. **Up-sell:** "Premium version" (higher-value alternatives)
4. **Alternatives:** "Out of stock? Try this" (similar products)
5. **Accessories:** "Complete your purchase" (complementary items)

### Reviews & Ratings (Future E-commerce)
- 5-star rating
- Review text + images
- Verified purchase badge
- Helpful votes
- Moderation workflow

### Media
**Images:** Primary + 20 gallery (1200×1200px, 5MB max), auto-thumbnails  
**Videos:** Embed YouTube/Vimeo (up to 5)  
**Documents:** PDF manuals, warranties, certificates  
**360° View:** Image sequence  

### Templates
- Pre-filled descriptions
- Category-specific attributes
- Common tags
- Speed up creation

### SEO (Future)
- Meta title (60 chars)
- Meta description (160 chars)
- URL slug (auto from name)
- Schema markup

### Workflow
**Status:** Draft → Pending Review → Approved → Published → Archived  
**Approval:** Optional workflow  
**Quality Checks:** Required fields, image, description length  

### Bulk Operations
- Bulk description/tag updates
- Bulk image upload (filename = SKU)
- Content export/import (Excel)

---

# PART D: BUSINESS INTELLIGENCE

## 15. Reports & Analytics

### Report Categories

**A. Sales Reports:**
Daily/hourly sales, Item-wise, Category-wise, Cashier-wise, Payment method-wise, Top-selling, Sales trends, By customer segment

**B. Inventory Reports:**
Stock summary, Movement, Dead stock, Fast/slow-moving, Low stock alerts, Valuation, Aging, Batch expiry

**C. Financial Reports:**
P&L, Balance Sheet, Cash Flow, Trial Balance, Day Book, Bank Book, Cash Book

**D. Tax Reports:**
GST summary (CGST/SGST/IGST), GSTR-1, GSTR-3B, TDS, ITC, Tax liability

**E. Customer Reports:**
Purchase history, Customer-wise sales, Loyalty points, Aging (receivables), Credit utilization, Top customers

**F. Employee Reports:**
Attendance summary, Sales performance, Commission, Payroll register, Leave balance, Late/early arrivals

**G. Purchase Reports:**
Purchase summary, Supplier-wise, Purchase vs sales, Pending POs, GRN, Payment due

**H. Reconciliation Reports:**
Cash, Payment gateway, Inventory, Bank, Settlement (opening-transactions-closing)

### Features
- **Scheduling:** Auto-generate and email (daily EOD, monthly P&L)
- **Custom Builder:** Drag-and-drop (select fields, filters, groupings)
- **Dashboards:** Real-time KPIs (sales, pending orders, low stock, top performers, cash)
- **Export:** PDF, Excel, CSV, JSON
- **Comparative:** Period-over-period (MoM, YoY), Branch vs branch
- **Drill-down:** Summary → Details → Transaction
- **Visualization:** Line, Bar, Pie charts, Tables

---

## 16. Reconciliation & Audit

### Reconciliation Types

**1. Cash:** EOD counting vs system cash sales (detect overages/shortages, denomination-wise)

**2. Payment Gateway:** Match UPI/Card with bank settlements (identify missing/duplicate)

**3. Inventory:** Physical count vs system (cycle counting or full audit, variance analysis)

**4. Inter-branch Transfer:** Sent items = received items (in-transit discrepancy tracking)

### Variance Handling (All 3)
- Auto-adjust with manager approval (PIN/OTP)
- Create adjustment entries with reason
- Investigation workflow (flag → investigate → resolve → approve → adjust)

### Audit Trail (All Implemented)
- **CRUD logging:** All Create/Read/Update/Delete on critical entities
- **User actions:** Login/logout, modifications, approvals, price changes
- **Retention:** 7 years (tax compliance)
- **Immutable:** Tamper-proof with checksums
- **Fields:** Who, What, When, Why, Before/After values

### Frequency
Configurable: Daily (cash), Weekly (inventory), Monthly (suppliers)

### Settlement Reports
Opening balance, All transactions, Closing balance (per POS, Branch, Merchant)

---

## 17. Notifications & Alerts

### Channels (All)
Email, SMS, WhatsApp, In-app, Push, System alerts

### Types & Triggers (All)

**A. Transactional:**
Sale receipts, Purchase orders, Payment confirmations, Invoice alerts

**B. Operational:**
Low stock, Reorder point, Pending approvals, Shift reminders, Stock expiry (30 days before)

**C. Financial:**
Payment due (7 days before), Overdue invoices, Daily sales summary (EOD), Bank reconciliation mismatches, Credit limit breach

**D. Employee:**
Payroll processed, Leave approval/rejection, Attendance irregularities, Target achievements, Commission payments

**E. System:**
Offline POS, Sync failures, Backup completion, License expiry (30/15/7 days), Database errors, Performance degradation

### Recipients (All)
- Role-based (all managers get low stock)
- User preferences (opt-in/opt-out per type)
- Escalation (if no response in X hours, notify admin)

### Templates
- Default provided
- Merchant customizable (branding, language, variables)
- Multi-language

### Scheduling
- **Real-time:** Critical (stock-out, payment failure, offline)
- **Batched:** Daily digest (all low stock in one email)
- Configurable per type

### Two-way
- Customer reply to payment reminders
- Confirm via SMS reply
- Click-to-call
- Link to portal for invoice view/payment

---

# PART E: SECURITY & SUBSCRIPTION

## 18. Authentication & Authorization

### Authentication (All Configurable)
- Username/Password (strength requirements)
- Email/Phone + OTP
- Biometric (fingerprint for POS)
- PIN (4-6 digits for quick cashier login)
- SSO (Google, Microsoft)

### Roles
**Default:** Merchant Admin, Branch Manager, POS User, Accountant, Inventory Manager, Sales Manager, HR Manager

**Custom:** Dynamic role creation at merchant/branch level with unlimited definitions

### Permissions (Hybrid)
- **Feature-level:** Access to "Inventory Module" (all-or-nothing)
- **Action-level:** View vs Create vs Update vs Delete granularity
- **Data-level:** Own branch vs all branches

**Examples:**
- View Products: Yes, Create: No
- Process Sales: Yes, Discounts >10%: No
- View Reports: Yes, Export: No

### Session Management
**Timeout (Recommended):**
- POS: 15 min
- Branch Manager: 30 min
- Admin: 60 min
- API: 24 hours (token-based)

**Security:**
- Concurrent login prevention (one device per user, except admins)
- Device binding for POS (registered devices only)
- "Remember device" (30-day trust)
- Force logout on password change
- Session revocation (admin kills all sessions)
- Audit all login attempts

**Password Policies:**
- Length: 8-12 characters
- Complexity: Upper, lower, number, special
- Expiry: 90 days (configurable)
- History: Cannot reuse last 5
- Lockout: 5 failed attempts, 15-min lockout

---

## 19. Subscription & Billing

### Pricing Tiers

**Freemium (Forever Free):**
1 Branch, 1 POS, 100 products, 500 transactions/month, Basic reports, Email support, 30-day retention

**Starter (₹999/POS/month):**
3 Branches, 5 POS, 5K products, Unlimited transactions, GST compliance, Basic integrations, 1-year retention

**Professional (₹1,999/POS/month):**
10 Branches, 25 POS, Unlimited products/transactions, Advanced reports, Custom builder, Full loyalty/CRM, API access, 3-year retention

**Enterprise (₹3,999/POS/month):**
Unlimited, Full payroll, Multi-currency, White-label, 24/7 support, SLA, Lifetime retention, Custom integrations

### Add-ons
- Extra storage: ₹500/month per 100GB
- SMS: ₹0.20/SMS
- WhatsApp: ₹0.50/message
- API calls: ₹1,000 per 10K (beyond limits)
- Premium support: ₹5,000/month
- Training: ₹