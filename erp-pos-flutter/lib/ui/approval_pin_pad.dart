import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Supervisor PIN entry (design system §06, Phase 12): masked dots plus a
/// large on-screen keypad for touch terminals, and the hardware keyboard
/// works too (digits, Backspace). Reports the PIN through [onChanged]; the
/// server is what checks it.
class ApprovalPinPad extends StatefulWidget {
  final ValueChanged<String> onChanged;
  final int maxLength;

  const ApprovalPinPad({super.key, required this.onChanged, this.maxLength = 6});

  @override
  State<ApprovalPinPad> createState() => _ApprovalPinPadState();
}

class _ApprovalPinPadState extends State<ApprovalPinPad> {
  String _pin = '';
  final _focus = FocusNode(debugLabel: 'pin-pad');

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  void _set(String pin) {
    setState(() => _pin = pin);
    widget.onChanged(pin);
  }

  void _digit(String d) {
    if (_pin.length < widget.maxLength) _set(_pin + d);
  }

  void _back() {
    if (_pin.isNotEmpty) _set(_pin.substring(0, _pin.length - 1));
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent e) {
    if (e is! KeyDownEvent && e is! KeyRepeatEvent) return KeyEventResult.ignored;
    final ch = e.character;
    if (ch != null && RegExp(r'^[0-9]$').hasMatch(ch)) {
      _digit(ch);
      return KeyEventResult.handled;
    }
    if (e.logicalKey == LogicalKeyboardKey.backspace) {
      _back();
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    Widget key(String label, {VoidCallback? onTap, Widget? child, String? semantics}) => Semantics(
          button: true,
          label: semantics ?? label,
          child: Material(
            color: scheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(10),
            child: InkWell(
              borderRadius: BorderRadius.circular(10),
              onTap: onTap,
              child: SizedBox(height: 52, child: Center(child: child ?? Text(label, style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w500)))),
            ),
          ),
        );

    return Focus(
      focusNode: _focus,
      onKeyEvent: _onKey,
      child: GestureDetector(
        onTap: _focus.requestFocus,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Semantics(
              label: 'PIN, ${_pin.length} digits entered',
              child: Row(
                key: const Key('pin-dots'),
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  for (var i = 0; i < 4 || i < _pin.length; i++)
                    Container(
                      margin: const EdgeInsets.symmetric(horizontal: 7, vertical: 8),
                      width: 14,
                      height: 14,
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        color: i < _pin.length ? scheme.onSurface : Colors.transparent,
                        border: Border.all(color: i < _pin.length ? scheme.onSurface : scheme.onSurfaceVariant, width: 2),
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 4),
            GridView.count(
              crossAxisCount: 3,
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              mainAxisSpacing: 8,
              crossAxisSpacing: 8,
              childAspectRatio: 2.2,
              children: [
                for (final d in ['1', '2', '3', '4', '5', '6', '7', '8', '9']) key(d, onTap: () => _digit(d)),
                key('Clear', onTap: () => _set(''), child: const Text('Clear')),
                key('0', onTap: () => _digit('0')),
                key('⌫', onTap: _back, child: const Icon(Icons.backspace_outlined), semantics: 'Delete last digit'),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
