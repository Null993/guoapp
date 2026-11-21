import 'dart:ui' show PointerDeviceKind;

import 'package:duanju_app/app_routes.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/home_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/page_tab_swipe.dart';
import 'package:duanju_app/settings_screen.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'library_feature_fixtures.dart';

class TrackingCatalogRepository extends LibraryFeatureRepository {
  int cachedReads = 0;
  @override
  Future<CatalogPage> cached(String source, {String category = ''}) {
    cachedReads++;
    return super.cached(source, category: category);
  }
}

void main() {
  testWidgets('cinema layout switches on the existing home route', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    final repository = TrackingCatalogRepository();
    final home = HomeScreen(repository: repository, store: store);
    await tester.pumpWidget(MaterialApp(home: home));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('toggle-search')), findsOneWidget);
    final reads = repository.cachedReads;

    Future<void> selectLayout(String label) async {
      await tester.tap(find.byTooltip('更多'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('设置与备份'));
      await tester.pumpAndSettle();
      final setting = find.byKey(const ValueKey('home-layout-setting'));
      await tester.ensureVisible(setting);
      await tester.tap(setting);
      await tester.pumpAndSettle();
      await tester.tap(find.text(label).last);
      await tester.pumpAndSettle();
      Navigator.of(tester.element(find.byType(SettingsScreen))).pop();
      await tester.pumpAndSettle();
    }

    await selectLayout('影院');
    expect(store.homeLayout, 'cinema');
    expect(find.byKey(const ValueKey('toggle-search')), findsNothing);
    expect(find.byType(TextField), findsOneWidget);
    expect(repository.cachedReads, reads);

    await selectLayout('经典');
    expect(store.homeLayout, 'classic');
    expect(find.byKey(const ValueKey('toggle-search')), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
  });

  testWidgets('page swipe is touch-only and ignores system back edges', (
    tester,
  ) async {
    final steps = <int>[];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: PageTabSwipe(onStep: steps.add, child: const SizedBox.expand()),
        ),
      ),
    );
    await tester.dragFrom(const Offset(400, 200), const Offset(-150, 0));
    await tester.dragFrom(const Offset(7, 200), const Offset(150, 0));
    await tester.dragFrom(
      const Offset(400, 200),
      const Offset(150, 0),
      kind: PointerDeviceKind.mouse,
    );
    await tester.dragFrom(const Offset(400, 200), const Offset(150, 0));
    expect(steps, [1, -1]);
  });

  testWidgets(
    'return from unchanged source management does not reload catalog',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      final repository = TrackingCatalogRepository();
      await tester.pumpWidget(
        MaterialApp(
          navigatorObservers: [appRouteObserver],
          home: HomeScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      final reads = repository.cachedReads;
      await tester.tap(find.byTooltip('更多'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('站源管理'));
      await tester.pumpAndSettle();
      expect(find.byType(SourcesScreen), findsOneWidget);
      await tester.tap(find.byTooltip('Back'));
      await tester.pumpAndSettle();
      await tester.pump(const Duration(milliseconds: 500));
      expect(find.byType(HomeScreen), findsOneWidget);
      expect(repository.cachedReads, reads);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );
}
