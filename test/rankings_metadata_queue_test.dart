import 'dart:async';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ranking_models.dart';
import 'package:duanju_app/rankings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'fixtures.dart';

class CountQueueRepository extends FixtureRepository {
  Completer<Drama?>? firstPending;
  bool failCounts = false;
  final supplementIds = <String>[];
  @override
  Future<List<RankingBoard>> rankingBoards() async => const [
    RankingBoard(id: 'hongguo-hot', source: 'hongguo', name: '总热播榜'),
  ];
  static Drama drama(int page, {int count = 3}) => Drama(
    id: 'hongguo:70000$page',
    source: 'hongguo',
    title: '合成第$page部',
    episodes: count,
  );
  @override
  Future<RankingPage> rankings(
    String board, {
    int page = 1,
    bool force = false,
  }) async => RankingPage(
    items: [RankingItem(page, drama(page))],
    page: page,
    hasMore: page == 1,
  );
  @override
  Future<Drama?> supplementMetadata(Drama item) async {
    supplementIds.add(item.id);
    if (failCounts) throw AppFailure('合成网络超时');
    if (item.id == drama(1).id && firstPending != null) {
      return firstPending!.future;
    }
    return drama(item.id == drama(1).id ? 1 : 2, count: 82);
  }
}

void main() {
  Future<LocalStore> mount(
    WidgetTester tester,
    CountQueueRepository repository,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    await tester.pumpWidget(
      MaterialApp(
        home: RankingsScreen(
          repository: repository,
          store: store,
          initialGroup: 'hongguo',
        ),
      ),
    );
    await tester.pumpAndSettle();
    return store;
  }

  testWidgets('count completion survives loading the next ranking page', (
    tester,
  ) async {
    final repository = CountQueueRepository()
      ..firstPending = Completer<Drama?>();
    final store = await mount(tester, repository);
    expect(find.text('共 3 集'), findsNothing);
    expect(find.text('集数待确认'), findsOneWidget);
    await tester.tap(find.text('加载更多'));
    await tester.pumpAndSettle();
    repository.firstPending!.complete(CountQueueRepository.drama(1, count: 82));
    await tester.pumpAndSettle();
    expect(find.text('共 82 集'), findsNWidgets(2));
    expect(repository.supplementIds.toSet(), {
      CountQueueRepository.drama(1).id,
      CountQueueRepository.drama(2).id,
    });
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
  });
  testWidgets(
    'count failures are visible and can be retried without opening details',
    (tester) async {
      final repository = CountQueueRepository()..failCounts = true;
      final store = await mount(tester, repository);
      expect(find.text('共 3 集'), findsNothing);
      expect(find.text('重试集数'), findsOneWidget);
      repository.failCounts = false;
      await tester.tap(find.text('重试集数'));
      await tester.pumpAndSettle();
      expect(find.text('共 82 集'), findsOneWidget);
      expect(find.text('重试集数'), findsNothing);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );
}
