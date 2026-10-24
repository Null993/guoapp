import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_preferences.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:duanju_app/video_enhancement_mpv.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'playback_test.dart' show DeviceFixtureRepository;

class EdgeFixtureRepository extends DeviceFixtureRepository {
  EdgeFixtureRepository(this.input);
  final String input;
  @override
  Future<void> initialize() async {}
  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async => PlaybackPlan(url: input, local: true);
  @override
  Future<void> cancelPlayback() async {}
  @override
  Future<void> release(String session) async {}
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'Windows portrait decoder and rendered edges preserve visible width',
    (tester) async {
      expect(Platform.isWindows, isTrue);
      expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
      MediaKit.ensureInitialized();
      await windowManager.ensureInitialized();
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      await store.setPlaybackPreferences(
        const PlaybackPreferences(danmaku: false),
      );
      final report = <Map<String, dynamic>>[];
      const directory = String.fromEnvironment('EDGE_FIXTURE_DIRECTORY');
      final output = Directory(
        const String.fromEnvironment('EDGE_REPORT_DIRECTORY'),
      );
      await output.create(recursive: true);
      for (final fixture in [
        ('edge-540x960', 540, 960),
        ('edge-1080x1920', 1080, 1920),
        ('edge-1920x1080', 1920, 1080),
        ('edge-1080x1920-hevc8', 1080, 1920),
        ('edge-1080x1920-hevc10', 1080, 1920),
      ]) {
        final (name, width, height) = fixture;
        final repository = EdgeFixtureRepository('$directory/$name.mp4');
        final root = GlobalKey();
        final detail = DramaDetail(DeviceFixtureRepository.drama, [
          Episode({'id': name, 'source': 'hongguo', 'currentEpisode': 1}, 1),
        ]);
        await windowManager.focus();
        await tester.pumpWidget(
          RepaintBoundary(
            key: root,
            child: MaterialApp(
              home: PlayerScreen(
                key: ValueKey(name),
                detail: detail,
                initialIndex: 0,
                repository: repository,
                store: store,
              ),
            ),
          ),
        );
        Player? player;
        VideoController? video;
        var rendered = false;
        final timer = Stopwatch()..start();
        while (timer.elapsed < const Duration(seconds: 30)) {
          await tester.pump(const Duration(milliseconds: 100));
          final found = find.byType(Video);
          if (found.evaluate().isEmpty) continue;
          video ??= tester.widget<Video>(found.first).controller;
          if (player == null) {
            player = video.player;
            video.waitUntilFirstFrameRendered.then((_) => rendered = true);
          }
          if (rendered && !player.state.playing && !player.state.buffering) {
            await windowManager.focus();
            final start = find.byTooltip('开始播放');
            if (start.evaluate().isNotEmpty) await tester.tap(start.first);
          }
          if (rendered && player.state.position.inMilliseconds > 3500) break;
        }
        expect(rendered, isTrue, reason: name);
        expect(
          player?.state.position.inMilliseconds,
          greaterThan(3500),
          reason: name,
        );
        expect(player!.state.width, width);
        expect(player.state.height, height);
        final mpv = VideoEnhancementMpv(player.platform as NativePlayer);
        await mpv.initialize();
        final decoderOption = mpv.read('hwdec');
        expect(
          decoderOption is List ? decoderOption.join(',') : decoderOption,
          'auto-copy',
        );
        final hwdec = mpv.read('hwdec-current')?.toString() ?? '';
        expect(
          hwdec == 'no' || hwdec.isEmpty || hwdec.endsWith('-copy'),
          isTrue,
          reason: 'Unexpected zero-copy decoder: $hwdec',
        );
        final png = await tester.runAsync(
          () => player!.screenshot(format: 'image/png'),
        );
        expect(png, isNotNull);
        final codec = await ui.instantiateImageCodec(png!);
        final frame = await codec.getNextFrame();
        final raw = (await frame.image.toByteData(
          format: ui.ImageByteFormat.rawRgba,
        ))!.buffer.asUint8List();
        expect(frame.image.width, width);
        expect(frame.image.height, height);
        for (final y in [height ~/ 4, height ~/ 2, height * 3 ~/ 4]) {
          final left = (y * width + 2) * 4;
          final right = (y * width + width - 3) * 4;
          expect(
            raw[left] - raw[left + 1],
            greaterThan(100),
            reason: '$name left border',
          );
          expect(
            raw[right + 2] - raw[right + 1],
            greaterThan(100),
            reason: '$name right border',
          );
        }
        frame.image.dispose();
        codec.dispose();
        await tester.pump();
        final boundary =
            root.currentContext!.findRenderObject()! as RenderRepaintBoundary;
        final capture = await boundary.toImage();
        final rgba = (await capture.toByteData(
          format: ui.ImageByteFormat.rawRgba,
        ))!.buffer.asUint8List();
        final box = tester.renderObject<RenderBox>(find.byType(Video));
        final origin = boundary.globalToLocal(box.localToGlobal(Offset.zero));
        final y = (origin.dy + box.size.height / 2).round().clamp(
          0,
          capture.height - 1,
        );
        var red = 0, blue = 0, green = 0;
        for (
          var x = origin.dx.ceil().clamp(0, capture.width - 1);
          x < (origin.dx + box.size.width).floor().clamp(0, capture.width);
          x++
        ) {
          final i = (y * capture.width + x) * 4;
          final r = rgba[i], g = rgba[i + 1], b = rgba[i + 2];
          if (r > g + 50 && r > b + 50) red++;
          if (b > r + 50 && b > g + 50) blue++;
          if (g > r + 50 && g > b + 50) green++;
        }
        await File('${output.path}/$name-rendered.png').writeAsBytes(
          (await capture.toByteData(
            format: ui.ImageByteFormat.png,
          ))!.buffer.asUint8List(),
        );
        capture.dispose();
        expect(
          red,
          greaterThan(0),
          reason: '$name left rendered border missing',
        );
        expect(
          blue,
          greaterThan(0),
          reason: '$name right rendered border missing',
        );
        expect(
          green,
          0,
          reason: '$name green pixels in neutral synthetic frame',
        );
        report.add({
          'fixture': name,
          'width': width,
          'height': height,
          'hwdec': mpv.read('hwdec'),
          'hwdecCurrent': hwdec,
          'firstFrameRendered': rendered,
          'positionMs': player.state.position.inMilliseconds,
          'renderedRedPixels': red,
          'renderedBluePixels': blue,
          'renderedGreenPixels': green,
        });
        await File(
          '${output.path}/edge-report.json',
        ).writeAsString(const JsonEncoder.withIndent('  ').convert(report));
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pump(const Duration(milliseconds: 500));
        expect(tester.takeException(), isNull);
      }
      store.dispose();
    },
    timeout: const Timeout(Duration(minutes: 4)),
  );
}
