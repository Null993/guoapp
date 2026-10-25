import 'package:duanju_app/app_routes.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/home_screen.dart';
import 'package:duanju_app/local_store.dart';
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
