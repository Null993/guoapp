import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/widgets.dart';
import 'package:duanju_app/video_enhancement_mpv.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

class ValidationPaths extends PathProviderPlatform {
  ValidationPaths(this.directory);
  final String directory;
  @override
  Future<String?> getApplicationSupportPath() async => directory;
}

String safeError(Object error) => error.toString().replaceAllMapped(
  RegExp(r'https?://[^\s]+'),
  (match) => '${Uri.tryParse(match[0]!)?.host ?? "remote"}/[redacted]',
);

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final selected = const String.fromEnvironment(
    'SOURCE_IDS',
  ).split(',').where((id) => id.isNotEmpty).toSet();
  final report = <String, dynamic>{
    'version': '0.2.76+83',
    'platform': Platform.operatingSystem,
    'startedAt': DateTime.now().toUtc().toIso8601String(),
    'requiredPerSource': 3,
    'criteria':
        'first rendered frame and at least 5 seconds of advancing playback',
    'imagesDisabled': const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'),
    'proxyMode': const String.fromEnvironment(
      'SOURCE_PROXY_MODE',
      defaultValue: 'auto',
    ),
    'sources': <Map<String, dynamic>>[],
  };
  File? output;
  late NativeRepository repository;
  late LocalStore store;
  var storeInitialized = false;

  Future<void> save() async {
    binding.reportData = report;
    await output?.writeAsString(
      const JsonEncoder.withIndent('  ').convert(report),
      flush: true,
    );
  }

  setUpAll(() async {
    expect(const bool.fromEnvironment('ALL_SOURCES'), isTrue);
    expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
    MediaKit.ensureInitialized();
    if (Platform.isWindows) await windowManager.ensureInitialized();
    final originalPaths = PathProviderPlatform.instance;
    final parent = (await originalPaths.getApplicationSupportPath())!;
    const reportPath = String.fromEnvironment('SOURCE_PLAYBACK_REPORT');
    output = File(
      reportPath.isEmpty ? '$parent/source-playback.json' : reportPath,
    );
    await output!.parent.create(recursive: true);
    const stateDirectory = String.fromEnvironment('SOURCE_STATE_DIRECTORY');
    final directory = Directory(
      stateDirectory.isEmpty
          ? '${output!.parent.path}/source-playback-state-${DateTime.now().millisecondsSinceEpoch}'
          : stateDirectory,
    );
    await save();
    if (stateDirectory.isNotEmpty) {
      expect(await directory.exists(), isTrue);
    }
    report['stateReused'] = stateDirectory.isNotEmpty;
    report['stateDirectory'] = directory.path;
    await directory.create(recursive: true);
    PathProviderPlatform.instance = ValidationPaths(directory.path);
    SharedPreferences.setMockInitialValues({});
    store = LocalStore(await SharedPreferences.getInstance());
    storeInitialized = true;
    repository = NativeRepository(background: true);
    await repository.initialize();
    final mode = report['proxyMode'] as String;
    expect(mode, anyOf('auto', 'direct', 'manual'));
    if (mode == 'manual') {
      const proxy = String.fromEnvironment('SOURCE_PROXY_URL');
      expect(Uri.tryParse(proxy)?.hasAuthority, isTrue);
      await repository.saveResourceSettings(
        const ResourceSettings(proxyMode: 'manual', proxyUrl: proxy),
      );
      report['proxyHost'] = Uri.parse(proxy).host;
    } else {
      await repository.saveResourceSettings(
        ResourceSettings(
          proxyMode: mode,
          proxyUrl: const String.fromEnvironment('SOURCE_PROXY_URL'),
        ),
      );
    }
    report['effectiveProxyMode'] =
        (await repository.resourceSettings()).proxyMode;
    await save();
  });

  for (final source in SourceSite.values) {
    if (selected.isNotEmpty && !selected.contains(source.id)) continue;
    testWidgets('3 different real titles: ${source.id}', (tester) async {
      final result = <String, dynamic>{
        'source': source.id,
        'name': source.name,
        'catalogType': source.description,
        'successCount': 0,
        'status': 'running',
        'catalogErrors': <String>[],
        'attempts': <Map<String, dynamic>>[],
      };
      (report['sources'] as List).add(result);
      await save();
      final seen = <String>{};
      final candidates = <Drama>[];
      for (final seed in const String.fromEnvironment(
        'SOURCE_SEED_IDS',
      ).split(',')) {
        final prefix = '${source.id}:';
        if (seed.startsWith(prefix) && seen.add(seed)) {
          candidates.add(
            Drama(
              id: seed,
              source: source.id,
              sourceId: seed.substring(prefix.length),
              title: seed,
            ),
          );
        }
      }
      for (var page = 1; page <= 3 && candidates.length < 20; page++) {
        CatalogPage? catalog;
        for (var retry = 0; retry < 2 && catalog == null; retry++) {
          try {
            catalog = await repository.catalog(
              source.id,
              page: page,
              force: true,
            );
          } catch (error) {
            (result['catalogErrors'] as List).add(safeError(error));
            await repository.cancelCatalog();
            await save();
          }
        }
        if (catalog == null) break;
        for (final drama in catalog.items) {
          if (drama.source == source.id &&
              drama.id.isNotEmpty &&
              seen.add(drama.id)) {
            candidates.add(drama);
          }
        }
        if (!catalog.hasMore) break;
      }
      result['catalogCandidates'] = candidates.length;
      debugPrint(
        'SOURCE_PLAYBACK ${source.id} candidates=${candidates.length}',
      );
      for (final drama in candidates.take(20)) {
        if ((result['successCount'] as int) >= 3) break;
        final attempt = <String, dynamic>{
          'id': drama.id,
          'title': drama.title,
          'success': false,
          'stage': 'detail',
          'startedAt': DateTime.now().toUtc().toIso8601String(),
        };
        (result['attempts'] as List).add(attempt);
        await save();
        final elapsed = Stopwatch()..start();
        Player? player;
        StreamSubscription<String>? errorSubscription;
        final errors = <String>[];
        var foregroundRestarts = 0;
        try {
          final detail = await repository.detail(drama);
          attempt['title'] = detail.drama.title;
          if (detail.episodes.isEmpty) throw StateError('No episodes');
          final episode = detail.episodes.first;
          attempt['episode'] = episode.number;
          attempt['episodeId'] = episode.id;
          attempt['stage'] = 'playback';
          if (Platform.isWindows) await windowManager.focus();
          await tester.pumpWidget(
            MaterialApp(
              home: PlayerScreen(
                key: ValueKey('${source.id}/${drama.id}'),
                detail: DramaDetail(detail.drama, [
                  episode,
                ], warning: detail.warning),
                initialIndex: 0,
                playerFactory: () => Player(
                  configuration: const PlayerConfiguration(
                    muted: true,
                    bufferSize: 32 * 1024 * 1024,
                    logLevel: MPVLogLevel.error,
                  ),
                ),
                repository: repository,
                store: store,
                allowOnlineFallback: true,
              ),
            ),
          );
          final playback = Stopwatch()..start();
          VideoController? video;
          int? initialPosition;
          var samples = 0;
          var rendered = false;
          while (playback.elapsed < const Duration(seconds: 65)) {
            await tester.pump(const Duration(milliseconds: 250));
            final failure = tester
                .widgetList<StatusPanel>(find.byType(StatusPanel))
                .where((panel) => panel.title == '暂时无法播放');
            if (failure.isNotEmpty) {
              attempt['error'] = safeError(failure.first.message);
              break;
            }
            final found = find.byType(Video);
            if (found.evaluate().isEmpty) continue;
            video ??= tester.widget<Video>(found.first).controller;
            if (player == null) {
              player = video.player;
              errorSubscription = player.stream.error.listen(
                (error) => errors.add(safeError(error)),
              );
              unawaited(
                video.waitUntilFirstFrameRendered.then((_) => rendered = true),
              );
            }
            final state = player.state;
            if (Platform.isWindows &&
                rendered &&
                !state.playing &&
                !state.buffering &&
                foregroundRestarts < 2 &&
                playback.elapsed > const Duration(seconds: 1)) {
              attempt['lifecycleBeforeResume'] = binding.lifecycleState?.name;
              await windowManager.focus();
              await tester.pump(const Duration(milliseconds: 100));
              final start = find.byTooltip('开始播放');
              if (start.evaluate().isNotEmpty) {
                await tester.tap(start.first);
                foregroundRestarts++;
                attempt['foregroundResumeClicks'] = foregroundRestarts;
              }
            }
            if (rendered &&
                (state.width ?? 0) > 0 &&
                state.playing &&
                !state.buffering) {
              final position = state.position.inMilliseconds;
              initialPosition ??= position;
              if (position > initialPosition) samples++;
              if (samples >= 3 && position - initialPosition >= 5000) {
                attempt.addAll({
                  'success': true,
                  'firstFrameRendered': true,
                  'width': state.width,
                  'height': state.height,
                  'startPositionMs': initialPosition,
                  'endPositionMs': position,
                  'advancingSamples': samples,
                });
                if (player.platform is NativePlayer) {
                  final mpv = VideoEnhancementMpv(
                    player.platform as NativePlayer,
                  );
                  await mpv.initialize();
                  attempt['hwdecOption'] = mpv.read('hwdec');
                  attempt['hwdecCurrent'] = mpv.read('hwdec-current');
                  attempt['videoOutParams'] = mpv.read('video-out-params');
                }
                result['successCount'] = (result['successCount'] as int) + 1;
                break;
              }
            }
          }
          if (attempt['success'] != true) {
            attempt['error'] ??=
                'No rendered frame with 5s playback advance within 65s';
            attempt['firstFrameRendered'] = rendered;
            attempt['lastPositionMs'] = player?.state.position.inMilliseconds;
            attempt['width'] = player?.state.width;
            attempt['buffering'] = player?.state.buffering;
            attempt['playing'] = player?.state.playing;
            attempt['lifecycle'] = binding.lifecycleState?.name;
          }
        } catch (error) {
          attempt['error'] = safeError(error);
        } finally {
          attempt['elapsedMs'] = elapsed.elapsedMilliseconds;
          if (errors.isNotEmpty) attempt['playerErrors'] = errors;
          await errorSubscription?.cancel();
          await tester.pumpWidget(const SizedBox.shrink());
          await tester.pump(const Duration(milliseconds: 500));
          final frameworkError = tester.takeException();
          if (frameworkError != null) {
            attempt['frameworkError'] = safeError(frameworkError);
            if (attempt['success'] == true) {
              attempt['success'] = false;
              result['successCount'] = (result['successCount'] as int) - 1;
            }
          }
          await repository.cancelPlayback();
          await save();
          debugPrint(
            'SOURCE_PLAYBACK ${source.id} ${drama.id} success=${attempt["success"]} total=${result["successCount"]}/3 elapsed=${attempt["elapsedMs"]}',
          );
        }
        final remaining =
            candidates.length.clamp(0, 20) -
            (result['attempts'] as List).length;
        if ((result['successCount'] as int) + remaining < 3) {
          result['stopReason'] =
              'Cannot reach 3 successes within candidate limit';
          break;
        }
      }
      result['status'] = (result['successCount'] as int) >= 3
          ? 'passed'
          : 'failed';
      result['finishedAt'] = DateTime.now().toUtc().toIso8601String();
      await save();
      expect(
        result['successCount'],
        greaterThanOrEqualTo(3),
        reason: '${source.name}: see source-playback report',
      );
    }, timeout: const Timeout(Duration(minutes: 50)));
  }
  tearDownAll(() async {
    report['finishedAt'] = DateTime.now().toUtc().toIso8601String();
    await save();
    if (storeInitialized) store.dispose();
  });
}
