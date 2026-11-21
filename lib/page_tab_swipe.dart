import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

class PageTabSwipe extends StatefulWidget {
  const PageTabSwipe({super.key, required this.onStep, required this.child});

  final ValueChanged<int>? onStep;
  final Widget child;

  @override
  State<PageTabSwipe> createState() => _PageTabSwipeState();
}

class _PageTabSwipeState extends State<PageTabSwipe> {
  static const _edge = 24.0;
  double? _distance;

  @override
  Widget build(BuildContext context) {
    if (widget.onStep == null) return widget.child;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      supportedDevices: const {
        PointerDeviceKind.touch,
        PointerDeviceKind.stylus,
        PointerDeviceKind.invertedStylus,
      },
      dragStartBehavior: DragStartBehavior.down,
      onHorizontalDragStart: (details) {
        final x = details.localPosition.dx;
        final width = context.size?.width ?? 0;
        _distance = x >= _edge && x <= width - _edge ? 0 : null;
      },
      onHorizontalDragUpdate: (details) {
        if (_distance != null) _distance = _distance! + details.delta.dx;
      },
      onHorizontalDragCancel: () => _distance = null,
      onHorizontalDragEnd: (_) {
        final distance = _distance;
        _distance = null;
        if (distance != null && distance.abs() >= 64) {
          widget.onStep?.call(distance < 0 ? 1 : -1);
        }
      },
      child: widget.child,
    );
  }
}
