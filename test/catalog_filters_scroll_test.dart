import 'package:duanju_app/catalog_filters.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final row = find.byKey(const ValueKey('catalog-categories'));
  ScrollPosition position(WidgetTester tester) => tester
      .state<ScrollableState>(
        find.descendant(of: row, matching: find.byType(Scrollable)),
      )
      .position;

  Future<ScrollController> mount(
    WidgetTester tester, {
    ValueChanged<String>? onCategory,
    TextDirection direction = TextDirection.ltr,
    int count = 30,
  }) async {
    final vertical = ScrollController();
    addTearDown(vertical.dispose);
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData(platform: TargetPlatform.windows),
        home: Directionality(
          textDirection: direction,
          child: Scaffold(
            body: SingleChildScrollView(
              controller: vertical,
              child: Column(
                children: [
                  CatalogFilters(
                    categories: List.generate(
                      count,
                      (index) => CatalogCategory('$index', '分类 $index'),
                    ),
                    category: '0',
                    onCategory: onCategory ?? (_) {},
                    onRetry: () {},
                  ),
                  const SizedBox(height: 1800),
                ],
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    return vertical;
  }

  for (final kind in [PointerDeviceKind.mouse, PointerDeviceKind.touch]) {
    testWidgets('$kind drags categories without selecting a chip', (
      tester,
    ) async {
      final selected = <String>[];
      final vertical = await mount(tester, onCategory: selected.add);
      final pointer = await tester.startGesture(
        tester.getCenter(row),
        kind: kind,
      );
      await pointer.moveBy(const Offset(-30, 0));
      await tester.pump(const Duration(milliseconds: 30));
      await pointer.moveBy(const Offset(-200, 0));
      await tester.pump(const Duration(milliseconds: 30));
      await pointer.up();
      await tester.pumpAndSettle();
      expect(position(tester).pixels, greaterThan(100));
      expect(vertical.offset, 0);
      expect(selected, isEmpty);
    });
  }

  testWidgets(
    'wheel scrolls categories once and leaves page scrolling outside',
    (tester) async {
      final vertical = await mount(tester);
      final center = tester.getCenter(row);
      tester.binding.handlePointerEvent(
        PointerScrollEvent(position: center, scrollDelta: const Offset(0, 120)),
      );
      await tester.pumpAndSettle();
      expect(position(tester).pixels, closeTo(120, .1));
      expect(vertical.offset, 0);
      tester.binding.handlePointerEvent(
        PointerScrollEvent(position: center, scrollDelta: const Offset(80, 0)),
      );
      await tester.pumpAndSettle();
      expect(position(tester).pixels, closeTo(200, .1));
      expect(vertical.offset, 0);
      await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
      tester.binding.handlePointerEvent(
        PointerScrollEvent(position: center, scrollDelta: const Offset(0, 120)),
      );
      await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
      await tester.pumpAndSettle();
      expect(position(tester).pixels, closeTo(320, .1));
      expect(vertical.offset, 0);
      tester.binding.handlePointerEvent(
        const PointerScrollEvent(
          position: Offset(200, 300),
          scrollDelta: Offset(0, 120),
        ),
      );
      await tester.pumpAndSettle();
      expect(vertical.offset, closeTo(120, .1));
      expect(position(tester).pixels, closeTo(320, .1));
    },
  );

  testWidgets('RTL wheel respects reading direction and clamps at the end', (
    tester,
  ) async {
    await mount(tester, direction: TextDirection.rtl);
    final center = tester.getCenter(row);
    tester.binding.handlePointerEvent(
      PointerScrollEvent(position: center, scrollDelta: const Offset(0, -120)),
    );
    await tester.pumpAndSettle();
    expect(position(tester).pixels, closeTo(120, .1));
    tester.binding.handlePointerEvent(
      PointerScrollEvent(
        position: center,
        scrollDelta: const Offset(0, -100000),
      ),
    );
    await tester.pumpAndSettle();
    expect(position(tester).pixels, position(tester).maxScrollExtent);
  });

  testWidgets(
    'click selects category and non-overflow row permits page wheel',
    (tester) async {
      final selected = <String>[];
      final vertical = await mount(tester, count: 2, onCategory: selected.add);
      await tester.tap(find.byKey(const ValueKey('category-1')));
      await tester.pumpAndSettle();
      expect(selected, ['1']);
      tester.binding.handlePointerEvent(
        PointerScrollEvent(
          position: tester.getCenter(row),
          scrollDelta: const Offset(0, 120),
        ),
      );
      await tester.pumpAndSettle();
      expect(vertical.offset, closeTo(120, .1));
      expect(position(tester).pixels, 0);
    },
  );
}
