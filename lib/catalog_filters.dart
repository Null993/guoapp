import 'dart:math';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import 'app_layout.dart';
import 'models.dart';
import 'remote_widgets.dart';

class CatalogFilters extends StatefulWidget {
  const CatalogFilters({
    super.key,
    required this.categories,
    required this.category,
    required this.onCategory,
    required this.onRetry,
    this.error,
    this.trailing,
  });

  final List<CatalogCategory> categories;
  final String category;
  final String? error;
  final ValueChanged<String> onCategory;
  final VoidCallback onRetry;
  final Widget? trailing;

  @override
  State<CatalogFilters> createState() => _CatalogFiltersState();
}

class _CatalogFiltersState extends State<CatalogFilters> {
  final _anchors = <String, GlobalKey>{};
  final _scroll = ScrollController();

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  void _scrollWithWheel(PointerSignalEvent event) {
    if (event is! PointerScrollEvent || !_scroll.hasClients) return;
    final position = _scroll.position;
    if (!position.hasContentDimensions) return;
    var delta = event.scrollDelta.dx != 0
        ? event.scrollDelta.dx
        : event.scrollDelta.dy;
    if (position.axisDirection == AxisDirection.left) delta = -delta;
    final target = (position.pixels + delta).clamp(
      position.minScrollExtent,
      position.maxScrollExtent,
    );
    if (target == position.pixels) return;
    GestureBinding.instance.pointerSignalResolver.register(event, (_) {
      if (mounted && _scroll.hasClients) _scroll.position.pointerScroll(delta);
    });
  }

  @override
  void didUpdateWidget(covariant CatalogFilters oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.category != widget.category) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final anchor = _anchors[widget.category]?.currentContext;
        if (mounted && anchor != null) {
          Scrollable.ensureVisible(
            anchor,
            alignment: .4,
            duration: const Duration(milliseconds: 180),
          );
        }
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final television = AppLayout.isTelevision(context);
    final behavior = ScrollConfiguration.of(context);
    return SizedBox(
      height: television
          ? 64
          : max(52, MediaQuery.textScalerOf(context).scale(14) + 28),
      child: Row(
        children: [
          Expanded(
            child: Listener(
              onPointerSignal: _scrollWithWheel,
              child: ScrollConfiguration(
                behavior: behavior.copyWith(
                  dragDevices: {
                    ...behavior.dragDevices,
                    PointerDeviceKind.mouse,
                  },
                ),
                child: SingleChildScrollView(
                  key: const ValueKey('catalog-categories'),
                  controller: _scroll,
                  scrollDirection: Axis.horizontal,
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  child: Row(
                    children: [
                      for (final entry in widget.categories)
                        Padding(
                          key: _anchors.putIfAbsent(entry.id, GlobalKey.new),
                          padding: const EdgeInsets.only(right: 6),
                          child: television
                              ? RemoteButton(
                                  key: ValueKey('category-${entry.id}'),
                                  label: entry.name,
                                  selected: entry.id == widget.category,
                                  onPressed: () => widget.onCategory(entry.id),
                                )
                              : ChoiceChip(
                                  key: ValueKey('category-${entry.id}'),
                                  label: Text(entry.name),
                                  selected: entry.id == widget.category,
                                  showCheckmark: false,
                                  onSelected: (_) =>
                                      widget.onCategory(entry.id),
                                ),
                        ),
                    ],
                  ),
                ),
              ),
            ),
          ),
          if (widget.error != null)
            IconButton(
              tooltip: widget.error,
              onPressed: widget.onRetry,
              icon: Icon(
                Icons.refresh_rounded,
                color: Theme.of(context).colorScheme.error,
              ),
            ),
          if (widget.trailing != null) widget.trailing!,
        ],
      ),
    );
  }
}
