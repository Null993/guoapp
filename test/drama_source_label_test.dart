import 'package:duanju_app/app_layout.dart';
import 'package:duanju_app/home_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/saved_library.dart';
import 'package:duanju_app/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'library_feature_fixtures.dart';

void main() {
  const drama = Drama(
    id: 'hongguo:label',
    source: 'hongguo',
    title: '弱点',
    category: 'AI 短剧',
    episodes: 30,
  );

  void viewport(WidgetTester tester, Size size, {double scale = 1}) {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = scale;
    addTearDown(tester.view.reset);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  }

  Future<LocalStore> createStore() async {
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  Future<void> mountTile(
    WidgetTester tester, {
    required Drama item,
    String? sourceName,
    double width = 160,
    double scale = 1,
  }) async {
    viewport(tester, const Size(390, 844), scale: scale);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.topLeft,
            child: SizedBox(
              width: width,
              child: DramaTile(
                drama: item,
                repository: LibraryFeatureRepository(),
                onTap: () {},
                sourceName: sourceName,
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('catalog card shows the source name right of the category', (
    tester,
  ) async {
    await mountTile(tester, item: drama, sourceName: '黄果旧版');
    final tile = find.byType(DramaTile);
    final category = find.descendant(of: tile, matching: find.text('AI 短剧'));
    final label = find.descendant(of: tile, matching: find.text('黄果旧版'));
    expect(category, findsOneWidget);
    expect(label, findsOneWidget);
    final row = find.ancestor(of: label, matching: find.byType(Row)).first;
    expect(
      find.descendant(of: row, matching: find.text('AI 短剧')),
      findsOneWidget,
    );
    expect(
      tester.getCenter(label).dy,
      closeTo(tester.getCenter(category).dy, 2),
    );
    expect(
      tester.getTopRight(label).dx,
      greaterThan(tester.getTopRight(category).dx),
    );
    expect(
      tester.getRect(tile).right - tester.getTopRight(label).dx,
      lessThanOrEqualTo(4),
    );
    expect(
      tester.widget<Text>(label).style!.fontSize,
      lessThanOrEqualTo(tester.widget<Text>(category).style!.fontSize!),
    );
    expect(
      tester.widget<Text>(label).style!.color,
      isNot(tester.widget<Text>(category).style!.color),
    );
    expect(find.text('共 30 集'), findsOneWidget);
    final coverBottom = tester.getRect(find.byType(DramaCover)).bottom;
    expect(tester.getRect(label).top, greaterThanOrEqualTo(coverBottom));
    expect(
      tester.getRect(label).top,
      greaterThanOrEqualTo(tester.getRect(find.text('共 30 集')).bottom),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'television catalog card labels the source below the category size',
    (tester) async {
      viewport(tester, const Size(1280, 720));
      await tester.pumpWidget(
        MaterialApp(
          home: AppLayout(
            television: true,
            child: Scaffold(
              body: Align(
                alignment: Alignment.topLeft,
                child: SizedBox(
                  width: 180,
                  child: DramaTile(
                    drama: drama,
                    repository: LibraryFeatureRepository(),
                    onTap: () {},
                    sourceName: '黄果 AI',
                  ),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final category = find.text('AI 短剧');
      final label = find.text('黄果 AI');
      expect(tester.widget<Text>(category).style!.fontSize, 14);
      expect(tester.widget<Text>(label).style!.fontSize, 12);
      expect(
        tester.getTopRight(label).dx,
        greaterThan(tester.getTopRight(category).dx),
      );
      final row = find.ancestor(of: label, matching: find.byType(Row)).first;
      expect(
        tester.getRect(row).right - tester.getTopRight(label).dx,
        lessThanOrEqualTo(1),
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('catalog card falls back to the built-in source name', (
    tester,
  ) async {
    await mountTile(tester, item: drama);
    expect(
      find.descendant(of: find.byType(DramaTile), matching: find.text('红果')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('catalog card hides the source name when it has no source', (
    tester,
  ) async {
    for (final source in ['', 'unknown-source']) {
      await mountTile(
        tester,
        item: Drama(
          id: 'hongguo:label',
          source: source,
          title: '弱点',
          category: 'AI 短剧',
          episodes: 30,
        ),
      );
      expect(find.text('红果'), findsNothing);
      final row = find
          .ancestor(of: find.text('AI 短剧'), matching: find.byType(Row))
          .first;
      expect(
        find.descendant(of: row, matching: find.byType(Text)),
        findsOneWidget,
      );
      expect(tester.takeException(), isNull);
    }
  });

  testWidgets('catalog card keeps narrow large text cards inside the row', (
    tester,
  ) async {
    await mountTile(
      tester,
      item: const Drama(
        id: 'hongguo:long',
        source: 'hongguo',
        title: '用于检查两行标题与海报对齐的合成短剧',
        category: '用于检查长分类文字的显示范围',
        episodes: 30,
      ),
      sourceName: '三十个字的超长自定义站源名称用于检查截断与换行行为是否正确',
      width: 100,
      scale: 1.8,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('home catalog card shows the customized source name', (
    tester,
  ) async {
    viewport(tester, const Size(390, 844));
    final store = await createStore();
    await store.configureSource('hongguo', name: '我的红果');
    await tester.pumpWidget(
      MaterialApp(
        home: HomeScreen(repository: LibraryFeatureRepository(), store: store),
      ),
    );
    await tester.pumpAndSettle();
    final tile = find.byKey(ValueKey(LibraryFeatureRepository.first.id));
    expect(tile, findsOneWidget);
    expect(
      find.descendant(of: tile, matching: find.text('我的红果')),
      findsOneWidget,
    );
    expect(find.descendant(of: tile, matching: find.text('红果')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('home catalog keeps a 320 wide large text grid inside the row', (
    tester,
  ) async {
    viewport(tester, const Size(320, 844), scale: 1.8);
    final store = await createStore();
    await store.configureSource('hongguo', name: '我的红果');
    await tester.pumpWidget(
      MaterialApp(
        home: HomeScreen(repository: LibraryFeatureRepository(), store: store),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(DramaTile), findsWidgets);
    expect(
      find.descendant(
        of: find.byType(DramaTile).first,
        matching: find.text('我的红果'),
      ),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('saved library tile keeps one source name for its subtitle', (
    tester,
  ) async {
    viewport(tester, const Size(390, 844));
    final store = await createStore();
    await store.toggleFavorite(LibraryFeatureRepository.first);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SavedLibrary(
            repository: LibraryFeatureRepository(),
            store: store,
            history: false,
            onOpen: (_) {},
            onContinue: (_) {},
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final tile = find.byKey(
      ValueKey('saved-${LibraryFeatureRepository.first.id}'),
    );
    expect(tile, findsOneWidget);
    expect(
      find.descendant(of: tile, matching: find.text('红果')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });
}
