import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'playback_test.dart' show DeviceFixtureRepository, fixtureBase;

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('native core and MP4 HLS CENC decode smoke', (tester) async {
    expect(fixtureBase, startsWith('http://127.0.0.1:'));
    expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
    MediaKit.ensureInitialized();
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    final repository = DeviceFixtureRepository();
    await repository.initialize();
    final detail = await repository.detail(DeviceFixtureRepository.drama);
    final samples = <Map<String, Object?>>[];
    for (var index = 0; index < 3; index++) {
      await tester.pumpWidget(
        MaterialApp(
          home: PlayerScreen(
            key: ValueKey('smoke-$index'),
            detail: detail,
            initialIndex: index,
            repository: repository,
            store: store,
          ),
        ),
      );
      final timer = Stopwatch()..start();
      Player? decoded;
      while (timer.elapsed < const Duration(seconds: 40)) {
        await tester.pump(const Duration(milliseconds: 100));
        final video = find.byType(Video);
        if (video.evaluate().isEmpty) continue;
        final player = tester.widget<Video>(video).controller.player;
        if ((player.state.width ?? 0) > 0 &&
            player.state.duration.inSeconds >= 18 &&
            player.state.position.inMilliseconds > 500) {
          decoded = player;
          break;
        }
      }
      expect(
        decoded,
        isNotNull,
        reason: 'episode ${index + 1} did not decode: ${repository.resolved}',
      );
      final video = tester.widget<Video>(find.byType(Video)).controller;
      await tester.runAsync(
        () => video.waitUntilFirstFrameRendered.timeout(
          const Duration(seconds: 15),
        ),
      );
      samples.add({
        'episode': index + 1,
        'width': decoded!.state.width,
        'height': decoded.state.height,
        'positionMs': decoded.state.position.inMilliseconds,
        'firstFrameRendered': true,
      });
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump(const Duration(milliseconds: 500));
    }
    store.dispose();
    binding.reportData = {'nativeCore': true, 'decodedMedia': samples};
  }, timeout: const Timeout(Duration(minutes: 4)));
}
