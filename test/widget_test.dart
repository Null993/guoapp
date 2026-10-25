import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/detail_screen.dart';
import 'package:duanju_app/follow_state.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_launch_screen.dart';
import 'package:duanju_app/remote_widgets.dart';
import 'package:duanju_app/app_build.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  Future<LocalStore> store() async {
    SharedPreferences.setMockInitialValues({});
    return LocalStore(await SharedPreferences.getInstance());
  }

  Future<LocalStore> gatedStore() async {
    SharedPreferences.setMockInitialValues({});
    final local = testStore(await SharedPreferences.getInstance());
    await local.enableSourceGate('666666');
    return local;
  }

  Future<void> settle(WidgetTester tester, [int frames = 20]) async {
    for (var index = 0; index < frames; index++) {
      await tester.pump(const Duration(milliseconds: 50));
    }
  }

  for (final size in [const Size(390, 844), const Size(1280, 800)]) {
    testWidgets('catalog, VIP filter and fresh details at $size', (
      tester,
    ) async {
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final repository = FixtureRepository();
      final local = await (allSourcesEnabled ? gatedStore() : store());
      await tester.pumpWidget(DuanjuApp(repository: repository, store: local));
      await settle(tester);
      expect(find.byTooltip('VIP：隐藏'), findsNothing);
      expect(find.text('测试短剧'), findsOneWidget);
      if (allSourcesEnabled) {
        await tester.tap(find.byKey(const ValueKey('source-switch')));
        await settle(tester);
        await tester.tap(
          find.descendant(
            of: find.byType(PopupMenuItem<SourceGroup>),
            matching: find.text('黄豆'),
          ),
        );
        await settle(tester);
        expect(find.text('会员测试剧'), findsNothing);
        expect(find.byTooltip('VIP：隐藏'), findsOneWidget);
        await tester.tap(find.byTooltip('VIP：隐藏'));
        await settle(tester);
        expect(find.byTooltip('VIP：显示'), findsOneWidget);
        expect(find.text('会员测试剧'), findsOneWidget);
        await tester.tap(find.byTooltip('VIP：显示'));
        await settle(tester);
      } else {
        expect(find.text('黄豆'), findsNothing);
      }
      expect(find.text('会员测试剧'), findsNothing);
      await tester.pumpWidget(
        MaterialApp(
          home: DetailScreen(
            drama: FixtureRepository.free,
            repository: repository,
            store: local,
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(repository.detailCalls, 1);
      if (find.text('展开').evaluate().isNotEmpty) {
        await tester.tap(find.text('展开'));
        await tester.pumpAndSettle();
      }
      expect(find.byKey(const ValueKey('episode-2')), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('follow-status')));
      await tester.pumpAndSettle();
      await tester.tap(find.text(FollowStatus.watching.label));
      await tester.pumpAndSettle();
      expect(local.isFavorite(FixtureRepository.free.id), isTrue);
      for (final episode in [1, 2]) {
        await local.saveWatch(
          WatchEntry(
            drama: FixtureRepository.free,
            episode: episode,
            position: 18.5,
            duration: 90,
            updatedAt: DateTime.now(),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('继续播放 · 第 $episode 集'), findsOneWidget);
        for (final number in [1, 2]) {
          final button = tester.widget<RemoteTarget>(
            find.descendant(
              of: find.byKey(ValueKey('episode-$number')),
              matching: find.byType(RemoteTarget),
            ),
          );
          expect(button.selected, number == episode);
        }
      }
      await local.clearHistory();
      await tester.pumpAndSettle();
      expect(find.text('立即播放'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('failed catalog can be retried', (tester) async {
    final repository = FixtureRepository()..fail = true;
    await tester.pumpWidget(
      DuanjuApp(repository: repository, store: await store()),
    );
    await tester.pumpAndSettle();
    expect(find.text('暂时无法加载'), findsOneWidget);
    repository.fail = false;
    await tester.tap(find.text('重试'));
    await tester.pumpAndSettle();
    expect(find.text('测试短剧'), findsOneWidget);
  });

  testWidgets('fresh disk catalog skips the network and resumes pagination', (
    tester,
  ) async {
    final repository = FixtureRepository();
    repository.cachedPages['hongguo'] = CatalogPage(
      [
        FixtureRepository.free,
        for (var index = 0; index < 30; index++)
          Drama(
            id: 'hongguo:cached-$index',
            source: 'hongguo',
            title: '缓存短剧$index',
            episodes: 1,
            category: '合成数据',
          ),
      ],
      fresh: true,
      page: 3,
      hasMore: true,
    );
    await tester.pumpWidget(
      DuanjuApp(repository: repository, store: await store()),
    );
    await tester.pumpAndSettle();
    expect(find.text('测试短剧'), findsOneWidget);
    expect(repository.requests, isEmpty);
    final scrollable = find.byType(CustomScrollView);
    for (var i = 0; i < 4 && repository.pages.isEmpty; i++) {
      await tester.drag(scrollable, const Offset(0, -900));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
    }
    await tester.pumpAndSettle();
    expect(repository.pages, [4]);
    await tester.tap(find.byKey(const ValueKey('catalog-refresh')));
    await tester.pumpAndSettle();
    expect(repository.pages, [4, 1]);
    expect(repository.forced, [false, true]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('stale disk catalog remains usable when refresh fails', (
    tester,
  ) async {
    final repository = FixtureRepository()..fail = true;
    repository.cachedPages['hongguo'] = CatalogPage([FixtureRepository.free]);
    await tester.pumpWidget(
      DuanjuApp(repository: repository, store: await store()),
    );
    await tester.pumpAndSettle();
    expect(find.text('测试短剧'), findsOneWidget);
    expect(find.text('合成网络错误'), findsNothing);
    expect(find.text('暂时无法加载'), findsNothing);
    expect(repository.requests, ['hongguo']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('launch page keeps its route when showing the resolved player', (
    tester,
  ) async {
    final repository = FixtureRepository();
    final local = await store();
    await tester.pumpWidget(
      MaterialApp(
        home: PlaybackLaunchScreen(
          drama: FixtureRepository.free,
          repository: repository,
          store: local,
          playerBuilder: (detail, index, position) {
            expect(detail.episodes, hasLength(2));
            expect(index, 0);
            expect(position, 0);
            return const Scaffold(body: Text('launch player'));
          },
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('launch player'), findsOneWidget);
    expect(find.text('正在进入播放'), findsNothing);
    expect(find.byType(PlaybackLaunchScreen), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  test('watch progress and favorites survive a new store', () async {
    final local = await store();
    await local.saveWatch(
      WatchEntry(
        drama: FixtureRepository.free,
        episode: 2,
        position: 18.5,
        duration: 90,
        updatedAt: DateTime.now(),
      ),
    );
    await local.toggleFavorite(FixtureRepository.free);
    final restored = LocalStore(local.preferences);
    expect(restored.watched(FixtureRepository.free.id)?.position, 18.5);
    expect(restored.watched(FixtureRepository.free.id)?.episode, 2);
    expect(restored.isFavorite(FixtureRepository.free.id), isTrue);
  });
}
