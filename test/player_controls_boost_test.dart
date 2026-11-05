import 'package:duanju_app/player_controls.dart';
import 'package:duanju_app/player_interactions.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';

import 'player_fixtures.dart';

Future<PlayerInteractions> mountControls(
  WidgetTester tester,
  Player player,
) async {
  final interactions = PlayerInteractions(
    player: player,
    available: () => true,
    baseSpeed: () => 1.0,
    onTogglePlayback: () {},
    onFullscreen: (_) {},
    onSeek: (_) async {},
  );
  await tester.pumpWidget(
    MaterialApp(
      theme: ThemeData.dark(),
      home: Scaffold(
        body: SizedBox(
          width: 390,
          height: 300,
          child: PlayerControls(
            player: player,
            interactions: interactions,
            enabled: true,
            fullscreen: false,
            showOnPlaybackReady: true,
            onFullscreen: () {},
            onBack: () {},
            onPrevious: null,
            onNext: null,
            title: '测试剧集',
            onTogglePlayback: () {},
            onEpisodes: () async {},
            onSpeed: () async {},
            onQuality: () async {},
            speed: 1.0,
            qualityLabel: '1080P',
            onFocusSurface: () {},
          ),
        ),
      ),
    ),
  );
  await tester.pump();
  return interactions;
}

double chromeOpacity(WidgetTester tester) => tester
    .widget<AnimatedOpacity>(
      find.descendant(
        of: find.byKey(const ValueKey('player-controls-chrome')),
        matching: find.byType(AnimatedOpacity),
      ),
    )
    .opacity;

bool chromeIgnoring(WidgetTester tester) => tester
    .widget<IgnorePointer>(find.byKey(const ValueKey('player-controls-chrome')))
    .ignoring;

void main() {
  testWidgets('长按倍速期间自动隐藏全部控件，松手后恢复原显隐状态', (tester) async {
    tester.view.physicalSize = const Size(390, 300);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final platform = ScriptedPlayer();
    final player = Player(platformPlayer: platform);
    await platform.playOrPause();
    final interactions = await mountControls(tester, player);

    expect(interactions.boosting, isFalse);
    expect(chromeOpacity(tester), 1.0);
    expect(chromeIgnoring(tester), isFalse);
    expect(find.textContaining('倍速中'), findsNothing);

    final gesture = await tester.startGesture(const Offset(195, 60));
    await tester.pump(const Duration(milliseconds: 400));
    expect(interactions.boosting, isTrue);

    expect(chromeIgnoring(tester), isTrue);
    await tester.pump(const Duration(milliseconds: 250));
    expect(chromeOpacity(tester), 0.0);

    expect(find.textContaining('倍速中'), findsOneWidget);

    await tester.pump(const Duration(seconds: 5));
    expect(chromeOpacity(tester), 0.0);
    expect(interactions.boosting, isTrue);

    await gesture.up();
    await tester.pump();
    expect(interactions.boosting, isFalse);
    await tester.pump(const Duration(milliseconds: 250));
    expect(chromeOpacity(tester), 1.0);
    expect(chromeIgnoring(tester), isFalse);

    await tester.pump(const Duration(seconds: 6));
  });

  testWidgets('倍速前控件已隐藏时，倍速结束后保持隐藏', (tester) async {
    tester.view.physicalSize = const Size(390, 300);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final platform = ScriptedPlayer();
    final player = Player(platformPlayer: platform);
    await platform.playOrPause();
    final interactions = await mountControls(tester, player);

    await tester.tapAt(const Offset(195, 60));
    await tester.pump(const Duration(milliseconds: 500));
    expect(chromeOpacity(tester), 0.0);

    final gesture = await tester.startGesture(const Offset(195, 60));
    await tester.pump(const Duration(milliseconds: 400));
    expect(interactions.boosting, isTrue);
    expect(chromeOpacity(tester), 0.0);
    expect(find.textContaining('倍速中'), findsOneWidget);

    await gesture.up();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 250));
    expect(interactions.boosting, isFalse);
    expect(chromeOpacity(tester), 0.0);

    await tester.pump(const Duration(seconds: 3));
  });
}
