part of '../pos_screen.dart';

// ---------------------------------------------------------------------
// Product pane (design system §06, pins 1 and 4): the scan field always
// has focus — a keyboard-wedge scanner types the barcode + Enter into it —
// and typed letters double as a search that filters the grid below.
// Online the grid uses the server's fuzzy search and category chips;
// offline it falls back to the local catalog cache (AppSession.browseProducts),
// so a cashier can still find an item without its barcode.
// ---------------------------------------------------------------------

class _ProductPane extends StatefulWidget {
  final TextEditingController scanController;
  final FocusNode scanFocus;
  final bool busy;
  final ValueChanged<String> onScanSubmitted;
  final ValueChanged<GridProduct> onAdd;

  const _ProductPane({
    required this.scanController,
    required this.scanFocus,
    required this.busy,
    required this.onScanSubmitted,
    required this.onAdd,
  });

  @override
  State<_ProductPane> createState() => _ProductPaneState();
}

class _ProductPaneState extends State<_ProductPane> {
  List<CatalogCategory> _categories = [];
  // The chosen top-level chip, and the subcategory chip under it if any.
  // The server includes subcategories in a category filter, so choosing
  // just the parent (Cricket) shows all of its products.
  String? _parentId;
  String? _subcategoryId;
  List<GridProduct> _items = [];
  bool _loading = true;
  Timer? _debounce;
  String _lastQuery = '';
  int _requestSeq = 0;

  @override
  void initState() {
    super.initState();
    widget.scanController.addListener(_onQueryChanged);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _loadCategories();
      _load();
    });
  }

  @override
  void dispose() {
    widget.scanController.removeListener(_onQueryChanged);
    _debounce?.cancel();
    super.dispose();
  }

  Future<void> _loadCategories() async {
    final session = context.read<AppSession>();
    if (session.offlineMode) return;
    try {
      final cats = await session.loadCategories();
      if (mounted) setState(() => _categories = cats);
    } catch (_) {
      // Chips are a convenience; the grid and search work without them.
    }
  }

  void _onQueryChanged() {
    final q = widget.scanController.text.trim();
    if (q == _lastQuery) return;
    _debounce?.cancel();
    // A scanner gun types a whole barcode in a few milliseconds and then
    // Enter; only a human pausing is worth a search request.
    _debounce = Timer(const Duration(milliseconds: 300), () => _load());
  }

  Future<void> _load() async {
    final session = context.read<AppSession>();
    final q = widget.scanController.text.trim();
    // Digits-only input is a barcode being scanned or typed, not a search.
    final query = RegExp(r'^\d{6,}$').hasMatch(q) ? '' : q;
    _lastQuery = q;
    final seq = ++_requestSeq;
    setState(() => _loading = true);
    final items = await session.browseProducts(query: query, categoryId: _subcategoryId ?? _parentId);
    if (!mounted || seq != _requestSeq) return; // a newer search superseded this one
    setState(() {
      _items = items;
      _loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final session = context.watch<AppSession>();
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            key: const Key('scan-field'),
            controller: widget.scanController,
            focusNode: widget.scanFocus,
            autofocus: true,
            // Never disabled: a disabled field drops focus, and the next
            // item from a scanner gun would then go nowhere. Submissions
            // are ignored while a previous scan is still being added.
            style: const TextStyle(fontSize: 17),
            decoration: InputDecoration(
              labelText: tr('Scan or enter barcode'),
              hintText: tr('or type a name / SKU to search'),
              prefixIcon: Icon(Icons.qr_code_scanner, color: theme.colorScheme.primary),
              suffixIcon: widget.busy
                  ? const Padding(
                      padding: EdgeInsets.all(14),
                      child: SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
                    )
                  : const Padding(padding: EdgeInsets.all(14), child: _KeyHint('F2')),
            ),
            onSubmitted: widget.onScanSubmitted,
          ),
          if (_categories.isNotEmpty && !session.offlineMode) ...[
            const SizedBox(height: 12),
            _chipRow([
              _chip(tr('All'), _parentId == null, () => _selectCategory(null)),
              for (final c in _categories.where((c) => c.parentId == null))
                _chip(c.name, _parentId == c.categoryId, () => _selectCategory(c.categoryId)),
            ]),
            if (_subcategories.isNotEmpty) ...[
              const SizedBox(height: 8),
              _chipRow([
                _chip(tr('All {0}', [_nameOf(_parentId!)]), _subcategoryId == null, () => _selectSubcategory(null)),
                for (final c in _subcategories) _chip(c.name, _subcategoryId == c.categoryId, () => _selectSubcategory(c.categoryId)),
              ]),
            ],
          ],
          const SizedBox(height: 12),
          Expanded(child: _grid(theme)),
        ],
      ),
    );
  }

  List<CatalogCategory> get _subcategories =>
      _parentId == null ? const [] : _categories.where((c) => c.parentId == _parentId).toList();

  String _nameOf(String id) => _categories.firstWhere((c) => c.categoryId == id).name;

  void _selectCategory(String? id) {
    setState(() {
      _parentId = id;
      _subcategoryId = null;
    });
    _load();
  }

  void _selectSubcategory(String? id) {
    setState(() => _subcategoryId = id);
    _load();
  }

  Widget _chipRow(List<Widget> chips) => SizedBox(
        height: 40,
        child: ListView(scrollDirection: Axis.horizontal, children: chips),
      );

  Widget _chip(String label, bool selected, VoidCallback onTap) => Padding(
        padding: const EdgeInsets.only(right: 8),
        child: ChoiceChip(label: Text(label), selected: selected, onSelected: (_) => onTap()),
      );

  Widget _grid(ThemeData theme) {
    if (_loading && _items.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_items.isEmpty) {
      final q = widget.scanController.text.trim();
      return Center(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Icon(Icons.search_off, size: 36, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(height: 8),
          Text(q.isEmpty ? tr('No products to show') : tr('No product matches “{0}”', [q]), style: theme.textTheme.titleSmall),
          const SizedBox(height: 4),
          Text(
            context.read<AppSession>().offlineMode
                ? tr('Offline: showing the catalog cached on this device.')
                : tr('Try a shorter name, the SKU, or scan the barcode.'),
            style: theme.textTheme.bodySmall,
          ),
        ]),
      );
    }
    return LayoutBuilder(builder: (context, constraints) {
      final columns = (constraints.maxWidth / 180).floor().clamp(2, 6);
      return GridView.builder(
        padding: const EdgeInsets.only(bottom: 16),
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: columns,
          mainAxisSpacing: 10,
          crossAxisSpacing: 10,
          mainAxisExtent: 112,
        ),
        itemCount: _items.length,
        itemBuilder: (context, i) => _ProductTile(item: _items[i], enabled: !widget.busy, onTap: () => widget.onAdd(_items[i])),
      );
    });
  }
}

class _ProductTile extends StatelessWidget {
  final GridProduct item;
  final bool enabled;
  final VoidCallback onTap;

  const _ProductTile({required this.item, required this.enabled, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final pos = theme.pos;
    final available = item.available;
    final out = available != null && available <= 0;
    final low = available != null && available > 0 && available <= 5;
    final stockText = available == null
        ? null
        : out
            ? tr('Out of stock')
            : low
                ? tr('{0} left', [formatQty(available.toString())])
                : tr('{0} in stock', [formatQty(available.toString())]);
    // migrations/063: the product's own colour, else an open-price item's
    // standard one, marks the tile — a tint, a coloured edge and (for an
    // open-price item) a label, so colour is never the only signal.
    final accent = tileAccent(item, pos);
    return Semantics(
      button: true,
      label: tr('{0}{1}, {2}{3}. Add to cart.', [item.lookup.productName, item.openPrice ? tr(', open price') : '', formatRupees(item.lookup.sellingPrice), stockText != null ? ', $stockText' : '']),
      excludeSemantics: true,
      child: Opacity(
        opacity: out ? 0.55 : 1,
        child: Material(
          key: accent == null ? null : Key('tile-accent-${item.lookup.variantId}'),
          color: accent == null ? theme.colorScheme.surface : Color.alphaBlend(accent.withValues(alpha: 0.12), theme.colorScheme.surface),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(12),
            side: BorderSide(color: accent ?? theme.colorScheme.outline, width: accent == null ? 1 : 2),
          ),
          child: InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: enabled ? onTap : null,
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(item.lookup.productName, maxLines: 2, overflow: TextOverflow.ellipsis, style: theme.textTheme.titleSmall),
                  Row(children: [
                    Flexible(
                      child: Text(item.lookup.sku,
                          maxLines: 1, overflow: TextOverflow.ellipsis, style: kMonoStyle.copyWith(fontSize: 11, color: theme.colorScheme.onSurfaceVariant)),
                    ),
                    if (item.openPrice) ...[
                      const SizedBox(width: 6),
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                        decoration: BoxDecoration(color: accent, borderRadius: BorderRadius.circular(6)),
                        child: Text(tr('Open price'),
                            key: const Key('open-price-badge'),
                            style: theme.textTheme.labelSmall?.copyWith(color: _onColor(accent!), fontWeight: FontWeight.w600)),
                      ),
                    ],
                  ]),
                  const Spacer(),
                  Row(
                    children: [
                      Expanded(
                        child: Text(formatRupees(item.lookup.sellingPrice),
                            style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
                      ),
                      if (stockText != null)
                        Text(stockText,
                            style: theme.textTheme.bodySmall?.copyWith(
                              color: out || low ? pos.warn : theme.colorScheme.onSurfaceVariant,
                              fontWeight: low ? FontWeight.w600 : null,
                            )),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A tile's colour (erp-core-go migrations/063): the product's own, else
/// the standard open-price colour for an open-price item, else none.
@visibleForTesting
Color? tileAccent(GridProduct item, PosColors pos) {
  final hex = item.lookup.posColor;
  if (hex != null) return Color(int.parse('FF${hex.substring(1)}', radix: 16));
  return item.openPrice ? pos.plum : null;
}

/// Black or white text, whichever reads on [c].
Color _onColor(Color c) => c.computeLuminance() > 0.45 ? Colors.black : Colors.white;
