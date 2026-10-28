import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import 'core_bridge.dart';
import 'catalog_filters.dart';
import 'media_pipeline.dart';
import 'models.dart';
import 'playback_video_configuration.dart';
import 'video_enhancement_mpv.dart';

Future<void> runPackageSmoke(List<String> arguments) async {
  if (arguments.length != 3) exit(2);
  final report = File(arguments[1]), input = arguments[2];
  Player? player;
  final evidence = <String, dynamic>{'ok': false};
  final subscriptions = <StreamSubscription<dynamic>>[];
  final errors = <String>[];
  try {
    final repository = NativeRepository();
    await repository.initialize();
    evidence['nativeCore'] = true;
    evidence['updateBridge'] =
        (await repository.appUpdate('status'))['repository'] ==
        'Null993/guoapp';
    if (evidence['updateBridge'] != true) throw StateError('更新核心未就绪');
    evidence.addAll(await _smokeCategoryScrolling());
    final executor = FFmpegExecutor();
    final probe = await executor.probe(input);
    verifyMediaDuration(probe, 3);
    evidence['ffprobe'] = true;
    final remuxed = File(
      '${report.parent.path}${Platform.pathSeparator}remuxed.mkv',
    );
    await executor.run([
      '-i',
      input,
      '-map',
      '0:v:0',
      '-c',
      'copy',
      remuxed.path,
    ]);
    verifyMediaDuration(await executor.probe(remuxed.path), 3);
    evidence['remux'] = true;
    player = Player(configuration: const PlayerConfiguration(muted: true));
    final video = VideoController(
      player,
      configuration: playbackVideoConfiguration(),
    );
    runApp(
      MaterialApp(
        home: Scaffold(
          body: Video(controller: video, controls: NoVideoControls),
        ),
      ),
    );
    await WidgetsBinding.instance.endOfFrame;
    await player.setAudioTrack(AudioTrack.no());
    final advancing = Completer<void>();
    subscriptions.add(
      player.stream.position.listen((time) {
        evidence['positionMs'] = time.inMilliseconds;
        if (time.inMilliseconds >= 400 && !advancing.isCompleted) {
          advancing.complete();
        }
      }),
    );
    subscriptions.add(
      player.stream.completed.listen((completed) {
        evidence['completed'] = completed;
        if (completed &&
            player!.state.duration.inMilliseconds >= 2000 &&
            !advancing.isCompleted) {
          advancing.complete();
        }
      }),
    );
    subscriptions.add(player.stream.error.listen(errors.add));
    await player.open(Media(remuxed.path));
    await advancing.future.timeout(const Duration(seconds: 20));
    await video.waitUntilFirstFrameRendered.timeout(
      const Duration(seconds: 15),
    );
    evidence['firstFrameRendered'] = true;
    evidence['width'] = player.state.width;
    evidence['height'] = player.state.height;
    final mpv = VideoEnhancementMpv(player.platform as NativePlayer);
    await mpv.initialize();
    final decoderOption = mpv.read('hwdec');
    evidence['hwdecOption'] = decoderOption is List
        ? decoderOption.join(',')
        : decoderOption;
    evidence['hwdecCurrent'] = mpv.read('hwdec-current');
    if (Platform.isWindows && evidence['hwdecOption'] != 'auto-copy') {
      throw StateError('Windows playback decoder policy not applied');
    }
    if (errors.isNotEmpty) throw StateError(errors.join('\n'));
    await report.writeAsString(
      jsonEncode({
        ...evidence,
        'ok': true,
        'nativeCore': true,
        'ffprobe': true,
        'remux': true,
        'mediaKitPlayback': true,
      }),
      flush: true,
    );
    await player.dispose();
    exit(0);
  } catch (error) {
    evidence['error'] = error.toString();
    evidence['playerErrors'] = errors;
    if (player != null) {
      evidence['durationMs'] = player.state.duration.inMilliseconds;
      evidence['lastPositionMs'] = player.state.position.inMilliseconds;
      evidence['playing'] = player.state.playing;
      evidence['buffering'] = player.state.buffering;
    }
    await player?.dispose();
    await report.writeAsString(jsonEncode(evidence), flush: true);
    exit(1);
  } finally {
    for (final subscription in subscriptions) {
      await subscription.cancel();
    }
  }
}

Future<Map<String, dynamic>> _smokeCategoryScrolling() async {
  final key = GlobalKey();
  final vertical = ScrollController();
  var selections = 0;
  try {
    runApp(
      MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            controller: vertical,
            child: Column(
              children: [
                CatalogFilters(
                  key: key,
                  categories: List.generate(
                    64,
                    (index) => CatalogCategory('$index', 'Category $index'),
                  ),
                  category: '0',
                  onCategory: (_) => selections++,
                  onRetry: () {},
                ),
                const SizedBox(height: 1800),
              ],
            ),
          ),
        ),
      ),
    );
    await WidgetsBinding.instance.endOfFrame;
    ScrollableState? scrollable;
    void visit(Element element) {
      if (element is StatefulElement && element.state is ScrollableState) {
        scrollable = element.state as ScrollableState;
      }
      element.visitChildElements(visit);
    }

    key.currentContext!.visitChildElements(visit);
    final position = scrollable!.position;
    final box = key.currentContext!.findRenderObject()! as RenderBox;
    final start = box.localToGlobal(
      Offset(box.size.width * .6, box.size.height / 2),
    );
    const device = 9901;
    void send(PointerEvent event) =>
        GestureBinding.instance.handlePointerEvent(event);
    send(
      PointerAddedEvent(
        device: device,
        kind: PointerDeviceKind.mouse,
        position: start,
      ),
    );
    send(
      PointerDownEvent(
        device: device,
        pointer: device,
        kind: PointerDeviceKind.mouse,
        position: start,
        buttons: kPrimaryMouseButton,
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 30));
    send(
      PointerMoveEvent(
        device: device,
        pointer: device,
        kind: PointerDeviceKind.mouse,
        position: start - const Offset(30, 0),
        delta: const Offset(-30, 0),
        buttons: kPrimaryMouseButton,
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 30));
    final end = start - const Offset(230, 0);
    send(
      PointerMoveEvent(
        device: device,
        pointer: device,
        kind: PointerDeviceKind.mouse,
        position: end,
        delta: const Offset(-200, 0),
        buttons: kPrimaryMouseButton,
      ),
    );
    send(
      PointerUpEvent(
        device: device,
        pointer: device,
        kind: PointerDeviceKind.mouse,
        position: end,
      ),
    );
    send(
      PointerRemovedEvent(
        device: device,
        kind: PointerDeviceKind.mouse,
        position: end,
      ),
    );
    final dragWorked = position.pixels > 100 && selections == 0;
    position.jumpTo(0);
    send(
      PointerScrollEvent(position: start, scrollDelta: const Offset(0, 120)),
    );
    final wheelWorked = (position.pixels - 120).abs() < 1;
    send(PointerScrollEvent(position: start, scrollDelta: const Offset(80, 0)));
    final horizontalWorked = (position.pixels - 200).abs() < 1;
    final pageStayedStill = vertical.offset == 0;
    if (!dragWorked || !wheelWorked || !horizontalWorked || !pageStayedStill) {
      throw StateError(
        'Category scrolling smoke failed: drag=$dragWorked wheel=$wheelWorked horizontal=$horizontalWorked page=$pageStayedStill',
      );
    }
    return {
      'categoryMouseDrag': dragWorked,
      'categoryMouseWheel': wheelWorked,
      'categoryHorizontalWheel': horizontalWorked,
      'categoryPageStayedStill': pageStayedStill,
    };
  } finally {
    runApp(const SizedBox.shrink());
    await WidgetsBinding.instance.endOfFrame;
    vertical.dispose();
  }
}
