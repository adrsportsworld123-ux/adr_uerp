import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../l10n/l10n.dart';

/// The till's own on-screen keyboard (erp-core-go migrations/072), for a
/// touch-screen POS with no physical keyboard. Switched on per terminal in
/// the back office (POS Terminals › Screen layout) and loaded with the
/// till's other screen settings; off, nothing changes.
///
/// [VirtualKeyboardHost] wraps the whole app (MaterialApp's builder), so it
/// serves every text field on every screen without the screens knowing:
/// it watches which field has focus, shows letters or a number pad to suit
/// the field's keyboard type, and types into it exactly as a hardware
/// keyboard would (the field's formatters and onChanged/onSubmitted run).
/// The keys never take focus, so the scan field keeps it; "hide" tucks the
/// keyboard away (a small button brings it back) until another field is
/// focused.
class PosKeyboard {
  PosKeyboard._();

  /// Whether this till shows the on-screen keyboard.
  static final enabled = ValueNotifier<bool>(false);
}

class VirtualKeyboardHost extends StatefulWidget {
  final Widget child;
  const VirtualKeyboardHost({super.key, required this.child});

  @override
  State<VirtualKeyboardHost> createState() => _VirtualKeyboardHostState();
}

enum _Layout { letters, symbols, numbers }

class _VirtualKeyboardHostState extends State<VirtualKeyboardHost> {
  EditableTextState? _target;
  FocusNode? _hiddenFor; // the field the cashier hid the keyboard for
  bool _shift = false;
  bool _symbols = false;

  @override
  void initState() {
    super.initState();
    FocusManager.instance.addListener(_focusChanged);
    PosKeyboard.enabled.addListener(_focusChanged);
    WidgetsBinding.instance.addPostFrameCallback((_) => _focusChanged());
  }

  @override
  void dispose() {
    FocusManager.instance.removeListener(_focusChanged);
    PosKeyboard.enabled.removeListener(_focusChanged);
    super.dispose();
  }

  void _focusChanged() {
    if (!mounted) return;
    final node = FocusManager.instance.primaryFocus;
    final target = PosKeyboard.enabled.value ? node?.context?.findAncestorStateOfType<EditableTextState>() : null;
    if (target == _target) return;
    setState(() {
      _target = target;
      _shift = false;
      _symbols = false;
      if (node != _hiddenFor) _hiddenFor = null;
    });
    // On a phone or tablet the system keyboard would open as well.
    if (target != null && (defaultTargetPlatform == TargetPlatform.android || defaultTargetPlatform == TargetPlatform.iOS)) {
      SystemChannels.textInput.invokeMethod<void>('TextInput.hide');
    }
  }

  EditableTextState? get _live {
    final t = _target;
    return t != null && t.mounted ? t : null;
  }

  void _edit(TextEditingValue Function(TextEditingValue v, TextSelection sel) change) {
    final t = _live;
    if (t == null || t.widget.readOnly) return;
    final v = t.textEditingValue;
    final sel = v.selection.isValid ? v.selection : TextSelection.collapsed(offset: v.text.length);
    t.userUpdateTextEditingValue(change(v, sel), SelectionChangedCause.keyboard);
  }

  void _type(String s) {
    final text = _shift ? s.toUpperCase() : s;
    _edit((v, sel) => TextEditingValue(
          text: v.text.replaceRange(sel.start, sel.end, text),
          selection: TextSelection.collapsed(offset: sel.start + text.length),
        ));
    if (_shift) setState(() => _shift = false);
  }

  void _backspace() {
    _edit((v, sel) {
      if (!sel.isCollapsed) {
        return TextEditingValue(text: v.text.replaceRange(sel.start, sel.end, ''), selection: TextSelection.collapsed(offset: sel.start));
      }
      if (sel.start == 0) return v;
      // One character (grapheme) back, not one UTF-16 unit.
      final before = v.text.substring(0, sel.start).characters.skipLast(1).toString();
      return TextEditingValue(text: before + v.text.substring(sel.start), selection: TextSelection.collapsed(offset: before.length));
    });
  }

  void _enter() {
    final t = _live;
    if (t == null) return;
    final action = t.widget.textInputAction ?? (t.widget.maxLines == 1 ? TextInputAction.done : TextInputAction.newline);
    if (action == TextInputAction.newline) {
      _type('\n');
    } else {
      t.performAction(action);
    }
  }

  _Layout _layoutFor(EditableTextState t) {
    final type = t.widget.keyboardType;
    if (type.index == TextInputType.number.index || type.index == TextInputType.phone.index) return _Layout.numbers;
    return _symbols ? _Layout.symbols : _Layout.letters;
  }

  @override
  Widget build(BuildContext context) {
    final t = _live;
    final node = FocusManager.instance.primaryFocus;
    final showing = t != null && node != _hiddenFor;
    return Column(
      children: [
        Expanded(
          child: Stack(
            children: [
              Positioned.fill(child: widget.child),
              if (t != null && !showing)
                PositionedDirectional(
                  end: 12,
                  bottom: 12,
                  child: TextFieldTapRegion(
                    child: Material(
                      elevation: 3,
                      shape: const CircleBorder(),
                      clipBehavior: Clip.antiAlias,
                      // No Tooltip: this sits above the Navigator's Overlay.
                      // A GestureDetector like the keys: it never takes focus.
                      child: Semantics(
                        container: true,
                        button: true,
                        label: tr('Show keyboard'),
                        child: GestureDetector(
                          behavior: HitTestBehavior.opaque,
                          onTap: () => setState(() => _hiddenFor = null),
                          child: const Padding(padding: EdgeInsets.all(12), child: Icon(Icons.keyboard)),
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
        if (showing)
          TextFieldTapRegion(
            child: _KeyboardPanel(
              layout: _layoutFor(t),
              shift: _shift,
              phone: t.widget.keyboardType.index == TextInputType.phone.index,
              onKey: _type,
              onBackspace: _backspace,
              onEnter: _enter,
              onShift: () => setState(() => _shift = !_shift),
              onSymbols: () => setState(() => _symbols = !_symbols),
              onHide: () => setState(() => _hiddenFor = node),
            ),
          ),
      ],
    );
  }
}

class _KeyboardPanel extends StatelessWidget {
  final _Layout layout;
  final bool shift;
  final bool phone;
  final void Function(String) onKey;
  final VoidCallback onBackspace, onEnter, onShift, onSymbols, onHide;

  const _KeyboardPanel({
    required this.layout,
    required this.shift,
    required this.phone,
    required this.onKey,
    required this.onBackspace,
    required this.onEnter,
    required this.onShift,
    required this.onSymbols,
    required this.onHide,
  });

  static const _letters = ['1234567890', 'qwertyuiop', 'asdfghjkl', 'zxcvbnm'];
  static const _symbolRows = ['1234567890', '@#₹&*()-+=', '/:;\'"!?%_', '<>[]{}~'];

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Material(
      color: scheme.surfaceContainerHighest,
      child: SafeArea(
        top: false,
        child: Directionality(
          // Key rows keep their order in a right-to-left screen.
          textDirection: TextDirection.ltr,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(6, 6, 6, 8),
            child: LayoutBuilder(
              builder: (context, c) {
                final keyHeight = c.maxWidth < 600 ? 44.0 : 52.0;
                return ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 1100),
                  child: layout == _Layout.numbers ? _numbers(keyHeight) : _qwerty(keyHeight),
                );
              },
            ),
          ),
        ),
      ),
    );
  }

  Widget _row(List<Widget> keys, double h) => SizedBox(height: h, child: Row(children: keys));

  Widget _char(String ch, {int flex = 2}) => _Key(label: shift && layout == _Layout.letters ? ch.toUpperCase() : ch, flex: flex, onTap: () => onKey(ch));

  Widget _qwerty(double h) {
    final rows = layout == _Layout.symbols ? _symbolRows : _letters;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        _row([for (final ch in rows[0].characters) _char(ch)], h),
        _row([for (final ch in rows[1].characters) _char(ch)], h),
        _row([const Spacer(), for (final ch in rows[2].characters) _char(ch), const Spacer()], h),
        _row([
          layout == _Layout.letters
              ? _Key(icon: shift ? Icons.keyboard_capslock : Icons.arrow_upward, label: tr('Shift'), iconOnly: true, flex: 3, selected: shift, onTap: onShift)
              : const Spacer(flex: 3),
          for (final ch in rows[3].characters) _char(ch),
          _Key(icon: Icons.backspace_outlined, label: tr('Backspace'), iconOnly: true, flex: 3, onTap: onBackspace, repeat: true),
        ], h),
        _row([
          _Key(label: layout == _Layout.symbols ? 'ABC' : '?123', flex: 3, onTap: onSymbols),
          _char(','),
          _Key(label: tr('Space'), flex: 10, onTap: () => onKey(' ')),
          _char('.'),
          _Key(icon: Icons.keyboard_return, label: tr('Enter'), iconOnly: true, flex: 3, onTap: onEnter, accent: true),
          _Key(icon: Icons.keyboard_hide_outlined, label: tr('Hide keyboard'), iconOnly: true, flex: 2, onTap: onHide),
        ], h),
      ],
    );
  }

  Widget _numbers(double h) {
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 420),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            _row([_char('1'), _char('2'), _char('3')], h),
            _row([_char('4'), _char('5'), _char('6')], h),
            _row([_char('7'), _char('8'), _char('9')], h),
            _row([
              phone ? _char('+') : _char('.'),
              _char('0'),
              _Key(icon: Icons.backspace_outlined, label: tr('Backspace'), iconOnly: true, flex: 2, onTap: onBackspace, repeat: true),
            ], h),
            _row([
              _Key(icon: Icons.keyboard_hide_outlined, label: tr('Hide keyboard'), iconOnly: true, flex: 2, onTap: onHide),
              _Key(icon: Icons.keyboard_return, label: tr('Enter'), iconOnly: true, flex: 4, onTap: onEnter, accent: true),
            ], h),
          ],
        ),
      ),
    );
  }
}

/// One key. A GestureDetector rather than a button, so a tap never moves
/// focus away from the field being typed into; holding backspace repeats.
class _Key extends StatefulWidget {
  final String label;
  final IconData? icon;
  final bool iconOnly;
  final int flex;
  final bool selected;
  final bool accent;
  final bool repeat;
  final VoidCallback onTap;

  const _Key({
    required this.label,
    required this.onTap,
    this.icon,
    this.iconOnly = false,
    this.flex = 2,
    this.selected = false,
    this.accent = false,
    this.repeat = false,
  });

  @override
  State<_Key> createState() => _KeyState();
}

class _KeyState extends State<_Key> {
  bool _down = false;
  bool _repeating = false;

  Future<void> _startRepeat() async {
    _repeating = true;
    await Future<void>.delayed(const Duration(milliseconds: 450));
    while (_repeating && mounted) {
      widget.onTap();
      await Future<void>.delayed(const Duration(milliseconds: 70));
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final bg = widget.accent
        ? scheme.primary
        : widget.selected
            ? scheme.secondaryContainer
            : (_down ? scheme.surfaceContainerHigh : scheme.surface);
    final fg = widget.accent ? scheme.onPrimary : scheme.onSurface;
    return Expanded(
      flex: widget.flex,
      child: Padding(
        padding: const EdgeInsets.all(3),
        child: Semantics(
          container: true,
          button: true,
          label: widget.label,
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTapDown: (_) => setState(() => _down = true),
            onTapCancel: () => setState(() => _down = false),
            onTapUp: (_) {
              setState(() => _down = false);
              widget.onTap();
            },
            onLongPressStart: widget.repeat ? (_) => _startRepeat() : null,
            onLongPressEnd: widget.repeat
                ? (_) {
                    _repeating = false;
                    setState(() => _down = false);
                  }
                : null,
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: bg,
                borderRadius: BorderRadius.circular(8),
                boxShadow: const [BoxShadow(color: Color(0x22000000), blurRadius: 1, offset: Offset(0, 1))],
              ),
              // The label is announced once, from the Semantics above.
              child: ExcludeSemantics(
                child: Center(
                  child: widget.icon != null && widget.iconOnly
                      ? Icon(widget.icon, size: 22, color: fg)
                      : Text(widget.label, style: TextStyle(fontSize: 18, fontWeight: FontWeight.w500, color: fg)),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
