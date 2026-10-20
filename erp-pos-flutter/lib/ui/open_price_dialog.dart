import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../state/session.dart';
import 'approval_pin_pad.dart';
import 'format.dart';

/// Adds [product] to the cart, asking for the price first when it's an
/// open-price item (Phase 13). The one entry point the scan field, the
/// product grid and full product search all use, so every way of picking
/// an item handles open price the same way. Returns whether a line was
/// added; on false, [AppSession.lastError] says why (null = the cashier
/// cancelled).
Future<bool> addProductToCart(BuildContext context, AppSession session, ProductLookup product, double quantity) async {
  var ok = await session.addToCart(product, quantity);
  var prompt = session.openPricePrompt;
  OpenPriceEntry? last;
  String? error;
  while (!ok && prompt != null) {
    if (!context.mounted) return false;
    final entry = await showDialog<OpenPriceEntry>(
      context: context,
      builder: (_) => OpenPriceDialog(prompt: prompt!, offline: session.offlineMode, error: error, initial: last),
    );
    if (entry == null) {
      session.openPricePrompt = null;
      return false; // cancelled: nothing to report
    }
    last = entry;
    ok = await session.addToCart(product, quantity, openPrice: entry);
    if (ok) break;
    if (session.openPricePrompt != null) {
      prompt = session.openPricePrompt; // e.g. the range changed meanwhile
      error = null;
    } else if (_retryable(session)) {
      // A wrong approver PIN reopens the keypad with the message, so the
      // manager can try again without re-scanning.
      error = session.lastError;
    } else {
      return false;
    }
  }
  return ok;
}

// Only an approval failure is worth reopening the keypad for; stock or
// network problems are reported once, as for any other item.
bool _retryable(AppSession s) => s.lastError?.startsWith('Approval failed') ?? false;

/// The price keypad for an open-price item: the allowed range, the
/// suggested price one tap away, and, once the typed price crosses the
/// approval threshold, the manager's employee code and PIN.
class OpenPriceDialog extends StatefulWidget {
  final OpenPricePrompt prompt;
  final bool offline;
  final String? error;
  final OpenPriceEntry? initial;

  const OpenPriceDialog({super.key, required this.prompt, this.offline = false, this.error, this.initial});

  @override
  State<OpenPriceDialog> createState() => _OpenPriceDialogState();
}

class _OpenPriceDialogState extends State<OpenPriceDialog> {
  late final _price = TextEditingController(text: widget.initial == null ? '' : _plain(widget.initial!.price));
  late final _approver = TextEditingController(text: widget.initial?.approverCode ?? '');
  late final _description = TextEditingController(text: widget.initial?.description ?? '');
  String _pin = '';

  static String _plain(double v) => v == v.roundToDouble() ? v.toStringAsFixed(0) : v.toStringAsFixed(2);

  @override
  void dispose() {
    _price.dispose();
    _approver.dispose();
    _description.dispose();
    super.dispose();
  }

  OpenPriceRange get _range => widget.prompt.range;
  double? get _value => double.tryParse(_price.text.trim());
  bool get _inRange => _value != null && _range.allows(_value!);
  bool get _needsApproval => _inRange && _range.needsApproval(_value!);

  String? get _problem {
    final v = _value;
    if (_price.text.trim().isEmpty) return null;
    if (v == null) return tr('Enter a number');
    if (!_range.allows(v)) return tr('Must be between {0} and {1}', [formatRupees(_range.min), formatRupees(_range.max)]);
    if (_needsApproval && widget.offline) return tr('Above {0} needs a manager\'s approval, which needs a connection', [formatRupees(_range.approvalAbove!)]);
    return null;
  }

  bool get _canAdd =>
      _inRange && _problem == null && (!_needsApproval || (_approver.text.trim().isNotEmpty && _pin.length >= 4));

  void _submit() {
    if (!_canAdd) return;
    Navigator.pop(
      context,
      OpenPriceEntry(_value!,
          approverCode: _needsApproval ? _approver.text.trim() : null,
          approverPin: _needsApproval ? _pin : null,
          description: _description.text.trim().isEmpty ? null : _description.text.trim()),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final suggested = double.tryParse(widget.prompt.suggestedPrice);
    return AlertDialog(
      title: Text(widget.prompt.product.productName),
      content: SizedBox(
        width: 360,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                tr('Open price · {0} to {1}{2}', [formatRupees(_range.min), formatRupees(_range.max), _range.approvalAbove == null ? '' : ' · ${tr('approval above {0}', [formatRupees(_range.approvalAbove!)])}']),
                style: theme.textTheme.bodySmall,
              ),
              const SizedBox(height: 12),
              TextField(
                key: const Key('open-price-field'),
                controller: _price,
                autofocus: true,
                keyboardType: const TextInputType.numberWithOptions(decimal: true),
                inputFormatters: [FilteringTextInputFormatter.allow(RegExp(r'^\d*\.?\d{0,2}'))],
                style: theme.textTheme.headlineSmall,
                textInputAction: TextInputAction.done,
                onChanged: (_) => setState(() {}),
                onSubmitted: (_) => _submit(),
                decoration: InputDecoration(
                  labelText: tr('Price'),
                  prefixText: '${PosCurrency.symbol} ',
                  border: const OutlineInputBorder(),
                  errorText: _problem,
                  errorMaxLines: 2,
                ),
              ),
              if (suggested != null && _range.allows(suggested))
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton(
                    onPressed: () => setState(() => _price.text = _plain(suggested)),
                    child: Text(tr('Use suggested {0}', [formatRupees(widget.prompt.suggestedPrice)])),
                  ),
                ),
              const SizedBox(height: 8),
              TextField(
                key: const Key('open-price-description'),
                controller: _description,
                maxLength: 120,
                textCapitalization: TextCapitalization.sentences,
                decoration: InputDecoration(
                  labelText: tr('Description (optional)'),
                  helperText: tr('Prints on the receipt instead of the item name'),
                  border: const OutlineInputBorder(),
                  isDense: true,
                ),
              ),
              if (_needsApproval && !widget.offline) ...[
                const SizedBox(height: 8),
                Text(tr('Manager approval'), style: theme.textTheme.titleSmall),
                const SizedBox(height: 8),
                TextField(
                  key: const Key('open-price-approver'),
                  controller: _approver,
                  textCapitalization: TextCapitalization.characters,
                  onChanged: (_) => setState(() {}),
                  decoration: InputDecoration(labelText: tr('Approver employee code'), border: const OutlineInputBorder(), isDense: true),
                ),
                const SizedBox(height: 8),
                ApprovalPinPad(onChanged: (pin) => setState(() => _pin = pin)),
              ],
              if (widget.error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(widget.error!, key: const Key('open-price-error'), style: TextStyle(color: theme.colorScheme.error)),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: Text(tr('Cancel'))),
        FilledButton(
          key: const Key('open-price-add'),
          onPressed: _canAdd ? _submit : null,
          child: Text(_value == null ? tr('Add') : tr('Add at {0}', [formatRupeesNum(_value!)])),
        ),
      ],
    );
  }
}
