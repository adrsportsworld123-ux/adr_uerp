import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../printing/printer_service.dart';
import '../state/session.dart';
import '../ui/approval_pin_pad.dart';
import '../ui/format.dart';

/// Product lookup at the till (Phase 13): price, stock here and in the
/// other branches, batches, and three actions that use the back office's
/// own endpoints and permissions:
/// - Edit price (pricing.manage; below cost needs a confirmed override).
///   Past sales are never re-priced.
/// - Adjust stock (inventory.adjust; a batch for batch-tracked items).
/// - Print label: Standard, Compact, Detailed (batch + expiry) or QR, to
///   the network printer receipts use.
/// An action the user lacks permission for is disabled with the reason.
class ProductLookupScreen extends StatefulWidget {
  final String productId;
  final String variantId;

  const ProductLookupScreen({super.key, required this.productId, required this.variantId});

  @override
  State<ProductLookupScreen> createState() => _ProductLookupScreenState();
}

class _ProductLookupScreenState extends State<ProductLookupScreen> {
  ProductDetailData? _product;
  VariantStockData? _stock;
  String? _error;
  final _printer = PrinterService();

  VariantDetail? get _variant {
    for (final v in _product?.variants ?? const <VariantDetail>[]) {
      if (v.variantId == widget.variantId) return v;
    }
    return null;
  }

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final session = context.read<AppSession>();
    try {
      final results = await Future.wait([session.productDetail(widget.productId), session.variantStock(widget.variantId)]);
      if (!mounted) return;
      setState(() {
        _product = results[0] as ProductDetailData;
        _stock = results[1] as VariantStockData;
        _error = null;
      });
      if (session.permissions == null) await session.refreshPermissions();
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = 'Can\'t reach the server. Product lookup needs a connection.');
    }
  }

  void _snack(String m) => ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(m)));

  Widget _scoped(Widget child) => ChangeNotifierProvider<AppSession>.value(value: context.read<AppSession>(), child: child);

  Future<void> _editPrice() async {
    final ok = await showDialog<bool>(context: context, builder: (_) => _scoped(_PriceDialog(variant: _variant!)));
    if (ok == true) {
      _snack('Price updated. Sales already made keep their price.');
      await _load();
    }
  }

  Future<void> _adjust() async {
    final ok = await showDialog<bool>(context: context, builder: (_) => _scoped(_AdjustDialog(variant: _variant!, batches: _stock!.batches)));
    if (ok == true) {
      _snack('Stock adjusted');
      await _load();
    }
  }

  Future<void> _label() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => _scoped(_LabelDialog(variant: _variant!, batches: _stock!.batches, printer: _printer)),
    );
    if (ok == true) _snack('Sent to the printer');
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final v = _variant;
    final here = _stock?.branches.where((b) => b.branchId == session.branchId).firstOrNull;
    final others = _stock?.branches.where((b) => b.branchId != session.branchId).toList() ?? const <BranchStock>[];

    Widget action(String label, IconData icon, String? permission, VoidCallback onTap, Key key) {
      final allowed = permission == null || session.can(permission);
      return Tooltip(
        message: allowed ? label : 'Needs the $permission permission',
        child: OutlinedButton.icon(key: key, onPressed: allowed ? onTap : null, icon: Icon(icon), label: Text(label)),
      );
    }

    return Scaffold(
      appBar: AppBar(title: Text(_product?.name ?? 'Product')),
      body: _error != null
          ? Center(child: Padding(padding: const EdgeInsets.all(24), child: Text(_error!, textAlign: TextAlign.center)))
          : v == null
              ? const Center(child: CircularProgressIndicator())
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView(padding: const EdgeInsets.all(16), children: [
                    Text('${v.sku}${v.barcode == null ? '' : ' · ${v.barcode}'}', style: theme.textTheme.bodySmall),
                    const SizedBox(height: 8),
                    Wrap(spacing: 24, runSpacing: 8, children: [
                      _fact(theme, 'Price', formatRupees(v.sellingPrice), key: const Key('lookup-price')),
                      _fact(theme, 'MRP', formatRupees(v.mrp)),
                      _fact(theme, 'In stock here', here == null ? '—' : formatQty(here.available), key: const Key('lookup-stock')),
                      if (v.status != 'active') _fact(theme, 'Status', statusLabel(v.status)),
                    ]),
                    if (v.openPrice != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 8),
                        child: Text(
                          'Open price: ${formatRupees(v.openPrice!.min)} to ${formatRupees(v.openPrice!.max)}'
                          '${v.openPrice!.approvalAbove == null ? '' : ', approval above ${formatRupees(v.openPrice!.approvalAbove!)}'}',
                          style: theme.textTheme.bodySmall,
                        ),
                      ),
                    const SizedBox(height: 16),
                    Wrap(spacing: 8, runSpacing: 8, children: [
                      action('Edit price', Icons.sell_outlined, 'pricing.manage', _editPrice, const Key('lookup-edit-price')),
                      action('Adjust stock', Icons.inventory_outlined, 'inventory.adjust', _adjust, const Key('lookup-adjust')),
                      action('Print label', Icons.print_outlined, null, _label, const Key('lookup-label')),
                    ]),
                    if (_stock!.batches.isNotEmpty) ...[
                      const SizedBox(height: 20),
                      Text('Batches here (sells soonest expiry first)', style: theme.textTheme.titleSmall),
                      for (final b in _stock!.batches)
                        ListTile(
                          dense: true,
                          contentPadding: EdgeInsets.zero,
                          title: Text(b.batchNo),
                          subtitle: Text(b.expiryDate == null ? 'No expiry' : 'Expires ${b.expiryDate}${b.expired ? ' · expired' : ''}',
                              style: b.expired ? TextStyle(color: theme.colorScheme.error) : null),
                          trailing: Text(formatQty(b.remaining)),
                        ),
                    ],
                    if (others.isNotEmpty) ...[
                      const SizedBox(height: 20),
                      Text('Other branches', style: theme.textTheme.titleSmall),
                      for (final b in others)
                        ListTile(
                          dense: true,
                          contentPadding: EdgeInsets.zero,
                          title: Text(b.branchName),
                          trailing: Text(formatQty(b.available)),
                        ),
                    ],
                  ]),
                ),
    );
  }

  Widget _fact(ThemeData theme, String label, String value, {Key? key}) => Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(label, style: theme.textTheme.labelMedium?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
        Text(value, key: key, style: theme.textTheme.titleLarge),
      ]);
}

final _money = [FilteringTextInputFormatter.allow(RegExp(r'^\d*\.?\d{0,2}'))];

class _PriceDialog extends StatefulWidget {
  final VariantDetail variant;
  const _PriceDialog({required this.variant});

  @override
  State<_PriceDialog> createState() => _PriceDialogState();
}

class _PriceDialogState extends State<_PriceDialog> {
  late final _price = TextEditingController(text: widget.variant.sellingPrice);
  final _reason = TextEditingController();
  bool _belowCost = false; // the server said NEGATIVE_MARGIN: ask to confirm
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _price.dispose();
    _reason.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final price = double.tryParse(_price.text);
    if (price == null || price <= 0) {
      setState(() => _error = 'Enter a price above zero');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await context.read<AppSession>().updateSellingPrice(widget.variant.variantId, price, _reason.text.trim(), override: _belowCost);
      if (mounted) Navigator.pop(context, true);
    } on ApiException catch (e) {
      setState(() {
        _busy = false;
        if (e.code == 'NEGATIVE_MARGIN') {
          _belowCost = true;
          _error = 'This is below the cost price (${formatRupees(widget.variant.costPrice)}). Save again to confirm.';
        } else {
          _error = e.statusCode == 403 ? 'You don\'t have permission to change prices.' : e.message;
        }
      });
    } catch (_) {
      setState(() {
        _busy = false;
        _error = 'Can\'t reach the server.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Edit selling price'),
      content: SizedBox(
        width: 360,
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          TextField(
            key: const Key('price-field'),
            controller: _price,
            autofocus: true,
            keyboardType: const TextInputType.numberWithOptions(decimal: true),
            inputFormatters: _money,
            onChanged: (_) => setState(() => _belowCost = false),
            decoration: InputDecoration(labelText: 'New price', prefixText: '${PosCurrency.symbol} ', border: const OutlineInputBorder()),
          ),
          const SizedBox(height: 8),
          TextField(
            key: const Key('price-reason'),
            controller: _reason,
            decoration: const InputDecoration(labelText: 'Reason (kept in price history)', border: OutlineInputBorder()),
          ),
          const SizedBox(height: 6),
          Text('Takes effect for new sales. Sales already made keep their price.', style: Theme.of(context).textTheme.bodySmall),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(_error!, key: const Key('price-error'), style: TextStyle(color: Theme.of(context).colorScheme.error)),
            ),
        ]),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          key: const Key('price-save'),
          onPressed: _busy ? null : _save,
          child: Text(_belowCost ? 'Save below cost' : 'Save'),
        ),
      ],
    );
  }
}

class _AdjustDialog extends StatefulWidget {
  final VariantDetail variant;
  final List<StockBatch> batches;
  const _AdjustDialog({required this.variant, required this.batches});

  @override
  State<_AdjustDialog> createState() => _AdjustDialogState();
}

class _AdjustDialogState extends State<_AdjustDialog> {
  bool _add = false;
  final _qty = TextEditingController();
  final _reason = TextEditingController();
  final _batchNo = TextEditingController();
  final _expiry = TextEditingController();
  String? _batchChoice; // an existing batch for a removal
  bool _busy = false;
  String? _error;
  // Phase 15: an adjustment worth more than the approval policy's stock
  // threshold needs a manager. The cost isn't known here, so the fields
  // appear once the server answers ADJUSTMENT_NOT_APPROVED.
  bool _needsApprover = false;
  final _approverCode = TextEditingController();
  String _pin = '';

  @override
  void dispose() {
    for (final c in [_qty, _reason, _batchNo, _expiry, _approverCode]) {
      c.dispose();
    }
    super.dispose();
  }

  bool get _needsBatch => widget.variant.trackBatch && _add;

  Future<void> _save() async {
    final q = double.tryParse(_qty.text);
    if (q == null || q <= 0 || _reason.text.trim().isEmpty || (_needsBatch && _batchNo.text.trim().isEmpty)) {
      setState(() => _error = _needsBatch ? 'Quantity, reason and batch number are required' : 'Quantity and reason are required');
      return;
    }
    if (_needsApprover && (_approverCode.text.trim().isEmpty || _pin.length < 4)) {
      setState(() => _error = 'Enter the approver\'s employee code and PIN');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final removalBatch = !_add && _batchChoice != null ? widget.batches.firstWhere((b) => b.batchId == _batchChoice).batchNo : null;
      await context.read<AppSession>().adjustStock(
            widget.variant.variantId,
            _add ? q : -q,
            _reason.text.trim(),
            batchNo: _needsBatch ? _batchNo.text.trim() : removalBatch,
            expiryDate: _needsBatch ? _expiry.text.trim() : null,
            approverCode: _needsApprover ? _approverCode.text.trim() : null,
            approverPin: _needsApprover ? _pin : null,
          );
      if (mounted) Navigator.pop(context, true);
    } on ApiException catch (e) {
      setState(() {
        _busy = false;
        if (e.code == 'ADJUSTMENT_NOT_APPROVED') {
          _error = _needsApprover
              ? 'Approval failed. Check the manager\'s employee code and PIN.'
              : 'An adjustment this size needs a manager\'s approval.';
          _needsApprover = true;
        } else {
          _error = e.statusCode == 403 ? 'You don\'t have permission to adjust stock.' : e.message;
        }
      });
    } catch (_) {
      setState(() {
        _busy = false;
        _error = 'Can\'t reach the server.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Adjust stock'),
      content: SizedBox(
        width: 380,
        child: SingleChildScrollView(
          child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            SegmentedButton<bool>(
              segments: const [
                ButtonSegment(value: false, label: Text('Remove'), icon: Icon(Icons.remove)),
                ButtonSegment(value: true, label: Text('Add'), icon: Icon(Icons.add)),
              ],
              selected: {_add},
              onSelectionChanged: (v) => setState(() => _add = v.first),
            ),
            const SizedBox(height: 12),
            TextField(
              key: const Key('adjust-qty'),
              controller: _qty,
              autofocus: true,
              keyboardType: const TextInputType.numberWithOptions(decimal: true),
              inputFormatters: [FilteringTextInputFormatter.allow(RegExp(r'^\d*\.?\d{0,3}'))],
              decoration: const InputDecoration(labelText: 'Quantity', border: OutlineInputBorder()),
            ),
            const SizedBox(height: 8),
            TextField(
              key: const Key('adjust-reason'),
              controller: _reason,
              decoration: const InputDecoration(labelText: 'Reason (damaged, found, recount ...)', border: OutlineInputBorder()),
            ),
            if (_needsBatch) ...[
              const SizedBox(height: 8),
              TextField(
                key: const Key('adjust-batch'),
                controller: _batchNo,
                decoration: const InputDecoration(labelText: 'Batch number', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 8),
              TextField(
                controller: _expiry,
                keyboardType: TextInputType.datetime,
                decoration: const InputDecoration(labelText: 'Expiry (YYYY-MM-DD, new batches)', border: OutlineInputBorder()),
              ),
            ],
            if (widget.variant.trackBatch && !_add && widget.batches.isNotEmpty) ...[
              const SizedBox(height: 8),
              DropdownButtonFormField<String?>(
                isExpanded: true,
                initialValue: _batchChoice,
                decoration: const InputDecoration(labelText: 'From batch', border: OutlineInputBorder()),
                items: [
                  const DropdownMenuItem<String?>(value: null, child: Text('Expired first (automatic)')),
                  for (final b in widget.batches)
                    DropdownMenuItem<String?>(value: b.batchId, child: Text('${b.batchNo} · ${formatQty(b.remaining)}', overflow: TextOverflow.ellipsis)),
                ],
                onChanged: (v) => setState(() => _batchChoice = v),
              ),
            ],
            if (_needsApprover) ...[
              const SizedBox(height: 12),
              TextField(
                key: const Key('adjust-approver-code'),
                controller: _approverCode,
                textCapitalization: TextCapitalization.characters,
                decoration: const InputDecoration(labelText: 'Approver employee code', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 8),
              ApprovalPinPad(onChanged: (pin) => setState(() => _pin = pin)),
            ],
            if (_error != null)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(_error!, key: const Key('adjust-error'), style: TextStyle(color: Theme.of(context).colorScheme.error)),
              ),
          ]),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(key: const Key('adjust-save'), onPressed: _busy ? null : _save, child: const Text('Adjust')),
      ],
    );
  }
}

class _LabelDialog extends StatefulWidget {
  final VariantDetail variant;
  final List<StockBatch> batches;
  final PrinterService printer;
  const _LabelDialog({required this.variant, required this.batches, required this.printer});

  @override
  State<_LabelDialog> createState() => _LabelDialogState();
}

const _templates = {
  'standard': 'Standard · 50×30',
  'compact': 'Compact · 40×20',
  'detailed': 'Detailed · 100×50, batch & expiry',
  'qr': 'QR · 50×50',
};

class _LabelDialogState extends State<_LabelDialog> {
  String _template = 'standard';
  int _copies = 1;
  String? _batchId;
  bool _busy = false;
  String? _error;

  Future<void> _print() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await context
          .read<AppSession>()
          .printLabel(widget.printer, widget.variant.variantId, _template, _copies, batchId: _template == 'detailed' ? _batchId : null);
      if (mounted) Navigator.pop(context, true);
    } on ApiException catch (e) {
      setState(() {
        _busy = false;
        _error = e.code == 'NO_BARCODE' ? 'This item has no barcode yet. Assign one in the back office.' : e.message;
      });
    } catch (e) {
      setState(() {
        _busy = false;
        _error = e is StateError ? e.message : 'Could not print: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Print label'),
      content: SizedBox(
        width: 380,
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          RadioGroup<String>(
            groupValue: _template,
            onChanged: (v) => setState(() => _template = v ?? 'standard'),
            child: Column(children: [
              for (final e in _templates.entries)
                RadioListTile<String>(
                  key: Key('template-${e.key}'),
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  value: e.key,
                  title: Text(e.value),
                ),
            ]),
          ),
          if (_template == 'detailed' && widget.batches.isNotEmpty)
            DropdownButtonFormField<String?>(
              isExpanded: true,
              initialValue: _batchId,
              decoration: const InputDecoration(labelText: 'Batch', border: OutlineInputBorder(), isDense: true),
              items: [
                const DropdownMenuItem<String?>(value: null, child: Text('The one that sells next')),
                for (final b in widget.batches)
                  DropdownMenuItem<String?>(
                      value: b.batchId, child: Text('${b.batchNo}${b.expiryDate == null ? '' : ' · exp ${b.expiryDate}'}', overflow: TextOverflow.ellipsis)),
              ],
              onChanged: (v) => setState(() => _batchId = v),
            ),
          const SizedBox(height: 8),
          Row(children: [
            const Text('Copies'),
            const Spacer(),
            IconButton(onPressed: _copies > 1 ? () => setState(() => _copies--) : null, icon: const Icon(Icons.remove), tooltip: 'Fewer'),
            Text('$_copies', key: const Key('label-copies'), style: Theme.of(context).textTheme.titleMedium),
            IconButton(onPressed: _copies < 100 ? () => setState(() => _copies++) : null, icon: const Icon(Icons.add), tooltip: 'More'),
          ]),
          if (_error != null) Text(_error!, key: const Key('label-error'), style: TextStyle(color: Theme.of(context).colorScheme.error)),
        ]),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton.icon(
          key: const Key('label-print'),
          onPressed: _busy ? null : _print,
          icon: const Icon(Icons.print),
          label: const Text('Print'),
        ),
      ],
    );
  }
}
