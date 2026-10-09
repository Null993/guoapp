import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';
import 'remote_test_helpers.dart';

const _synopsisDrama = Drama(
  id: 'hongguo:100',
  source: 'hongguo',
  title: '简介测试剧',
  description: '这是合成简介正文。',
  episodes: 2,
  category: '合成数据',
);

void main() {
  Future<void> settle(WidgetTester tester) async {
    for (var index = 0; index < 12; index++) {
      await tester.pump(const Duration(milliseconds: 10));
    }
  }

  void size(WidgetTester tester, Size value) {
    tester.view.physicalSize = value;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
  }

  Widget remoteHost(Widget child) => MaterialApp(
    theme: ThemeData.dark(),
    builder: (_, inner) => Shortcuts(
      shortcuts: const {
        SingleActivator(LogicalKeyboardKey.select): ActivateIntent(),
      },
      child: FocusTraversalGroup(child: inner!),
    ),
    home: child,
  );

  Future<ScriptedPlayer> build(
    WidgetTester tester,
    RouteRepository repository, {
    Drama drama = _synopsisDrama,
    bool remote = false,
  }) async {
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final player = ScriptedPlayer();
    final detail = await repository.detail(drama);
    final screen = PlayerScreen(
      detail: detail,
      initialIndex: 0,
      initialPosition: 7,
      repository: repository,
      store: store,
      playerFactory: () => Player(platformPlayer: player),
      videoBuilder: (controls) => controls,
    );
    await tester.pumpWidget(
      remote
          ? remoteHost(screen)
          : MaterialApp(theme: ThemeData.dark(), home: screen),
    );
    await settle(tester);
    return player;
  }

  Future<void> unmount(WidgetTester tester, ScriptedPlayer player) async {
    await tester.pumpWidget(const SizedBox.shrink());
    await settle(tester);
    expect(player.disposed, isTrue);
    expect(tester.takeException(), isNull);
  }

  testWidgets(
    'side panel switches between episodes and synopsis without restarting playback',
    (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      try {
        size(tester, const Size(1280, 800));
        final repository = RouteRepository();
        final player = await build(tester, repository);
        expect(
          find.byKey(const ValueKey('player-panel-tab-0')),
          findsOneWidget,
        );
        expect(
          find.byKey(const ValueKey('player-panel-tab-1')),
          findsOneWidget,
        );
        expect(find.byKey(const ValueKey('play-episode-2')), findsOneWidget);
        expect(find.text('定位当前'), findsOneWidget);
        expect(find.byTooltip('跳转集数'), findsOneWidget);
        final opened = player.opened.length;
        final played = player.played.length;
        final position = player.state.position;
        await tester.tap(find.byKey(const ValueKey('player-panel-tab-1')));
        await settle(tester);
        expect(
          find.byKey(const ValueKey('player-panel-synopsis')),
          findsOneWidget,
        );
        expect(find.text('这是合成简介正文。'), findsOneWidget);
        expect(find.byKey(const ValueKey('play-episode-2')), findsNothing);
        expect(find.text('定位当前'), findsNothing);
        expect(player.opened.length, opened);
        expect(player.played.length, played);
        expect(player.state.position, position);
        expect(player.state.playing, isTrue);
        await tester.tap(find.byKey(const ValueKey('player-panel-tab-0')));
        await settle(tester);
        expect(find.byKey(const ValueKey('play-episode-2')), findsOneWidget);
        expect(find.text('定位当前'), findsOneWidget);
        expect(
          find.byKey(const ValueKey('player-panel-synopsis')),
          findsNothing,
        );
        expect(player.opened.length, opened);
        expect(player.state.position, position);
        await unmount(tester, player);
      } finally {
        debugDefaultTargetPlatformOverride = null;
      }
    },
  );

  testWidgets('synopsis tab shows a readable placeholder without an intro', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.windows;
    try {
      size(tester, const Size(1280, 800));
      final repository = RouteRepository();
      final player = await build(
        tester,
        repository,
        drama: FixtureRepository.free,
      );
      await tester.tap(find.byKey(const ValueKey('player-panel-tab-1')));
      await settle(tester);
      expect(
        find.byKey(const ValueKey('player-panel-synopsis')),
        findsOneWidget,
      );
      expect(find.text('暂无简介'), findsOneWidget);
      await unmount(tester, player);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets(
    'remote focus moves between the side panel tabs and switches them by select',
    (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      try {
        size(tester, const Size(1280, 720));
        final repository = RouteRepository();
        final player = await build(tester, repository, remote: true);
        final opened = player.opened.length;
        final position = player.state.position;
        expect(
          find.byKey(const ValueKey('player-panel-tab-0')),
          findsOneWidget,
        );
        expect(
          find.byKey(const ValueKey('player-panel-tab-1')),
          findsOneWidget,
        );
        focusRemote(tester, find.byKey(const ValueKey('player-panel-tab-0')));
        await settle(tester);
        await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
        await settle(tester);
        await tester.sendKeyEvent(
          LogicalKeyboardKey.select,
          platform: 'android',
        );
        await settle(tester);
        expect(
          find.byKey(const ValueKey('player-panel-synopsis')),
          findsOneWidget,
        );
        expect(find.text('这是合成简介正文。'), findsOneWidget);
        expect(player.opened.length, opened);
        expect(player.state.position, position);
        await tester.sendKeyEvent(LogicalKeyboardKey.arrowLeft);
        await settle(tester);
        await tester.sendKeyEvent(
          LogicalKeyboardKey.select,
          platform: 'android',
        );
        await settle(tester);
        expect(find.byKey(const ValueKey('play-episode-2')), findsOneWidget);
        expect(player.opened.length, opened);
        await unmount(tester, player);
      } finally {
        debugDefaultTargetPlatformOverride = null;
      }
    },
  );
}
