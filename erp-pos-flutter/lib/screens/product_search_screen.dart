import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../state/session.dart';

/// Name/category/subcategory product lookup, pushed from PosScreen's AppBar
/// alongside the barcode field — the barcode field stays the fast path for
/// scanner-driven checkout; this is for "I don't have a barcode to scan, or
/// I don't know the SKU" (browsing by category, or a cashier searching a
/// product by name). Tapping a result adds it to the cart directly via
/// AppSession.addToCart and stays open, so a cashier can add several items
/// from one search/browse session without re-opening this screen each time.
class ProductSearchScreen extends StatefulWidget {
  const ProductSearchScreen({super.key});

  @override
  State<ProductSearchScreen> createState() => _ProductSearchScreenState();
}

class _ProductSearchScreenState extends State<ProductSearchScreen> {
  final _queryController = TextEditingController();
  List<CatalogCategory> _categories = [];
  String? _categoryId;
  String? _subcategoryId;
  List<ProductSearchResult> _results = [];
  bool _loading = false;
  String? _addingVariantId;
  String? _error;

  List<CatalogCategory> get _topLevelCategories => _categories.where((c) => c.parentId == null).toList();
  List<CatalogCategory> get _subcategories =>
      _categoryId == null ? const [] : _categories.where((c) => c.parentId == _categoryId).toList();

  @override
  void initState() {
    super.initState();
    _loadCategories();
    _search();
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

  Future<void> _addToCart(ProductSearchResult r) async {
    setState(() => _addingVariantId = r.variantId);
    final session = context.read<AppSession>();
    final ok = await session.addToCart(r.toProductLookup(), 1);
    if (!mounted) return;
    setState(() => _addingVariantId = null);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(ok ? 'Added ${r.name}' : (session.lastError ?? 'Could not add to cart'))),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Find a product')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
            child: TextField(
              controller: _queryController,
              autofocus: true,
              decoration: InputDecoration(
                labelText: 'Search by name or SKU',
                border: const OutlineInputBorder(),
                suffixIcon: IconButton(icon: const Icon(Icons.search), onPressed: _search),
              ),
              onSubmitted: (_) => _search(),
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                Expanded(
                  child: DropdownButtonFormField<String?>(
                    initialValue: _categoryId,
                    decoration: const InputDecoration(labelText: 'Category', border: OutlineInputBorder(), isDense: true),
                    items: [
                      const DropdownMenuItem<String?>(value: null, child: Text('All categories')),
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
                    initialValue: _subcategoryId,
                    decoration: const InputDecoration(labelText: 'Subcategory', border: OutlineInputBorder(), isDense: true),
                    items: [
                      const DropdownMenuItem<String?>(value: null, child: Text('All subcategories')),
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
              child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
            ),
          Expanded(
            child: _results.isEmpty && !_loading
                ? const Center(child: Text('No products match'))
                : ListView.builder(
                    itemCount: _results.length,
                    itemBuilder: (context, i) {
                      final r = _results[i];
                      final adding = _addingVariantId == r.variantId;
                      return ListTile(
                        title: Text(r.name),
                        subtitle: Text('${r.sku}${r.categoryName != null ? ' · ${r.categoryName}' : ''}'),
                        trailing: SizedBox(
                          width: 96,
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.end,
                            children: [
                              Text('₹${r.sellingPrice.toStringAsFixed(2)}'),
                              const SizedBox(width: 8),
                              adding
                                  ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
                                  : IconButton(
                                      icon: const Icon(Icons.add_circle_outline),
                                      tooltip: 'Add to cart',
                                      onPressed: () => _addToCart(r),
                                    ),
                            ],
                          ),
                        ),
                        onTap: adding ? null : () => _addToCart(r),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }
}
