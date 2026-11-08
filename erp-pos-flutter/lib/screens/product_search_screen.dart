import '../l10n/l10n.dart';
import '../ui/format.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../state/session.dart';
import '../ui/open_price_dialog.dart';
import 'product_lookup_screen.dart';

/// Name/category/subcategory product lookup — the barcode field stays the
/// fast path for scanner-driven checkout; this is for "I don't have a
/// barcode to scan, or I don't know the SKU" (browsing by category, or a
/// cashier searching a product by name). Tapping a result adds it to the
/// cart directly via AppSession.addToCart and stays open, so a cashier can
/// add several items from one search/browse session.
///
/// Phase 13: with [lookupMode] it is the POS's product lookup instead: a
/// tap (or a scanned barcode) opens the product's price, stock and label
/// screen rather than adding it to the cart. In normal mode the info
/// button on each row opens the same screen.
class ProductSearchScreen extends StatelessWidget {
  final bool lookupMode;

  const ProductSearchScreen({super.key, this.lookupMode = false});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(lookupMode ? tr('Product lookup') : tr('Find a product'))),
      body: ProductSearchView(lookupMode: lookupMode),
    );
  }
}

/// The search itself. PosScreen shows it in place of the product grid
/// ([onClose] set), so the cart stays on screen while the cashier
/// searches; [ProductSearchScreen] shows it as a page of its own.
class ProductSearchView extends StatefulWidget {
  final bool lookupMode;

  /// Embedded in the billing screen: a header with a close button (and
  /// Esc) instead of an app bar.
  final VoidCallback? onClose;

  /// Embedded: a barcode scanned or typed into the search adds that item,
  /// as the scan field would, instead of searching for the digits.
  final ValueChanged<String>? onBarcode;
  final FocusNode? focusNode;

  const ProductSearchView({super.key, this.lookupMode = false, this.onClose, this.onBarcode, this.focusNode});

  @override
  State<ProductSearchView> createState() => _ProductSearchViewState();
}

class _ProductSearchViewState extends State<ProductSearchView> {
  final _queryController = TextEditingController();
  List<CatalogCategory> _categories = [];
  String? _categoryId;
  String? _subcategoryId;
  List<ProductSearchResult> _results = [];
  bool _loading = false;
  String? _addingVariantId;
  String? _error;

  static final _barcode = RegExp(r'^\d{6,}$');

  List<CatalogCategory> get _topLevelCategories => _categories.where((c) => c.parentId == null).toList();
  List<CatalogCategory> get _subcategories =>
      _categoryId == null ? const [] : _categories.where((c) => c.parentId == _categoryId).toList();

  @override
  void initState() {
    super.initState();
    _loadCategories();
    _search();
    // Embedded, the scan field had focus; the search box takes it.
    if (widget.focusNode != null) WidgetsBinding.instance.addPostFrameCallback((_) => widget.focusNode!.requestFocus());
  }

  @override
  void dispose() {
    _queryController.dispose();
    super.dispose();
  }

  Future<void> _loadCategories() async {
    final cats = await context.read<AppSession>().loadCategories();
    if (!mounted) return;
    setState(() => _categories = cats);
  }

  Future<void> _search() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final session = context.read<AppSession>();
    // A selected subcategory narrows further than its parent category —
    // send whichever is more specific, never both (category_id is a
    // single exact-match filter server-side, not a subtree match).
    final effectiveCategoryId = _subcategoryId ?? _categoryId;
    final results = await session.searchProducts(query: _queryController.text.trim(), categoryId: effectiveCategoryId);
    if (!mounted) return;
    setState(() {
      _results = results;
      _loading = false;
      _error = results.isEmpty ? session.lastError : null;
    });
  }

  Future<void> _openLookup(String productId, String variantId) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ChangeNotifierProvider<AppSession>.value(
        value: context.read<AppSession>(),
        child: ProductLookupScreen(productId: productId, variantId: variantId),
      ),
    ));
  }

  /// Lookup mode: a scanned barcode goes straight to the product; anything
  /// else is a name/SKU search.
  Future<void> _lookupSubmitted() async {
    final q = _queryController.text.trim();
    if (_barcode.hasMatch(q)) {
      final product = await context.read<AppSession>().scanBarcode(q);
      if (product != null && mounted) {
        await _openLookup(product.productId, product.variantId);
        return;
      }
    }
    await _search();
  }

  void _submitted() {
    final q = _queryController.text.trim();
    if (widget.lookupMode) {
      _lookupSubmitted();
    } else if (widget.onBarcode != null && _barcode.hasMatch(q)) {
      _queryController.clear();
      widget.onBarcode!(q);
    } else {
      _search();
    }
  }

  Future<void> _addToCart(ProductSearchResult r) async {
    setState(() => _addingVariantId = r.variantId);
    final session = context.read<AppSession>();
    final ok = await addProductToCart(context, session, r.toProductLookup(), 1);
    if (!mounted) return;
    setState(() => _addingVariantId = null);
    if (!ok && session.lastError == null) return; // price entry cancelled
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(ok ? tr('Added {0}', [r.name]) : (session.lastError ?? tr('Could not add to cart')))),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final embedded = widget.onClose != null;
    final view = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (embedded)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 8, 0),
            child: Row(children: [
              Icon(Icons.manage_search, color: theme.colorScheme.primary),
              const SizedBox(width: 8),
              Expanded(child: Text(tr('Find a product'), style: theme.textTheme.titleMedium)),
              TextButton.icon(
                key: const Key('close-product-search'),
                onPressed: widget.onClose,
                icon: const Icon(Icons.close),
                label: Text(tr('Back to items (Esc)')),
              ),
            ]),
          ),
        Padding(
          padding: EdgeInsets.fromLTRB(16, embedded ? 8 : 16, 16, 0),
          child: TextField(
            key: const Key('product-search-field'),
            controller: _queryController,
            focusNode: widget.focusNode,
            autofocus: widget.focusNode == null,
            decoration: InputDecoration(
              labelText: tr('Search by name or SKU'),
              border: const OutlineInputBorder(),
              suffixIcon: IconButton(icon: const Icon(Icons.search), onPressed: _search),
            ),
            onSubmitted: (_) => _submitted(),
          ),
        ),
        Padding(
          padding: const EdgeInsets.all(16),
          child: Row(
            children: [
              Expanded(
                child: DropdownButtonFormField<String?>(
                  initialValue: _categoryId,
                  isExpanded: true,
                  decoration: InputDecoration(labelText: tr('Category'), border: const OutlineInputBorder(), isDense: true),
                  items: [
                    DropdownMenuItem<String?>(value: null, child: Text(tr('All categories'))),
                    for (final c in _topLevelCategories) DropdownMenuItem<String?>(value: c.categoryId, child: Text(c.name)),
                  ],
                  onChanged: (v) {
                    setState(() {
                      _categoryId = v;
                      _subcategoryId = null; // a new category invalidates whatever subcategory was picked under the old one
                    });
                    _search();
                  },
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: DropdownButtonFormField<String?>(
                  key: ValueKey(_categoryId), // a new category starts the subcategory over
                  initialValue: _subcategoryId,
                  isExpanded: true,
                  decoration: InputDecoration(labelText: tr('Subcategory'), border: const OutlineInputBorder(), isDense: true),
                  items: [
                    DropdownMenuItem<String?>(value: null, child: Text(tr('All subcategories'))),
                    for (final c in _subcategories) DropdownMenuItem<String?>(value: c.categoryId, child: Text(c.name)),
                  ],
                  onChanged: _categoryId == null
                      ? null
                      : (v) {
                          setState(() => _subcategoryId = v);
                          _search();
                        },
                ),
              ),
            ],
          ),
        ),
        if (_loading) const LinearProgressIndicator(),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
          ),
        Expanded(
          child: _results.isEmpty && !_loading
              ? Center(child: Text(tr('No products match')))
              : ListView.builder(
                  itemCount: _results.length,
                  itemBuilder: (context, i) {
                    final r = _results[i];
                    final adding = _addingVariantId == r.variantId;
                    return ListTile(
                      key: Key('search-result-${r.variantId}'),
                      title: Text(r.name),
                      subtitle: Text('${r.sku}${r.categoryName != null ? ' · ${r.categoryName}' : ''}'),
                      trailing: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          // The price, and the MRP under it to tell look-alike items apart.
                          Column(
                            mainAxisAlignment: MainAxisAlignment.center,
                            crossAxisAlignment: CrossAxisAlignment.end,
                            children: [
                              Text(formatRupeesNum(r.sellingPrice),
                                  style: theme.textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
                              if (r.mrp > 0)
                                Text(tr('MRP {0}', [formatRupeesNum(r.mrp)]),
                                    key: Key('search-mrp-${r.variantId}'),
                                    style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
                            ],
                          ),
                          const SizedBox(width: 8),
                          if (widget.lookupMode)
                            const Icon(Icons.chevron_right)
                          else if (adding)
                            const SizedBox(width: 48, child: Center(child: SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))))
                          else
                            IconButton(
                              icon: const Icon(Icons.add_circle_outline),
                              tooltip: tr('Add to cart'),
                              onPressed: () => _addToCart(r),
                            ),
                        ],
                      ),
                      leading: widget.lookupMode
                          ? null
                          : IconButton(
                              icon: const Icon(Icons.info_outline),
                              tooltip: tr('Price, stock and label'),
                              onPressed: () => _openLookup(r.productId, r.variantId),
                            ),
                      onTap: widget.lookupMode ? () => _openLookup(r.productId, r.variantId) : (adding ? null : () => _addToCart(r)),
                    );
                  },
                ),
        ),
      ],
    );
    if (!embedded) return view;
    return CallbackShortcuts(
      bindings: {const SingleActivator(LogicalKeyboardKey.escape): widget.onClose!},
      child: view,
    );
  }
}
