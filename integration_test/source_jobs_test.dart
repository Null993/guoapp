import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:duanju_app/app_layout.dart';
import 'package:duanju_app/background_downloads.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:duanju_app/source_login_screen.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'source_playback_test.dart' show ValidationPaths;

Map<String, dynamic> jobStatus(SourceStatus value) => {
  'stage': value.stage,
  'operation': value.operation,
  'running': value.running,
  'count': value.count,
  'page': value.page,
  'hasMore': value.hasMore,
  'completed': value.completed,
  'total': value.total,
  'error': value.error,
  'storageError': value.storageError,
  'resumable': value.resumable,
  'startedAt': value.startedAt?.toUtc().toIso8601String(),
  'finishedAt': value.finishedAt?.toUtc().toIso8601String(),
  'retryAt': value.retryAt?.toUtc().toIso8601String(),
  if (value.health case final health?)
    'health': {
      'state': health.state,
      'sample': health.sample,
      'steps': [
        for (final step in health.steps)
          {
            'name': step.name,
            'state': step.state,
            'message': step.message,
            'host': step.host,
            'httpStatus': step.httpStatus,
            'elapsedMs': step.elapsedMs,
          },
      ],
    },
};

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final report = <String, dynamic>{
    'version': '$appVersion+$appBuildCode',
    'platform': Platform.operatingSystem,
    'kind': 'imported progress and normal source-management buttons',
    'proxyMode': const String.fromEnvironment(
      'SOURCE_PROXY_MODE',
      defaultValue: 'auto',
    ),
    'allowInsecureTLS': const bool.fromEnvironment('SOURCE_ALLOW_INSECURE_TLS'),
    'ok': false,
    'sources': <Map<String, dynamic>>[],
  };
  late NativeRepository repository;
  late LocalStore store;
  late Directory state;
  File? output;
  Future<void> save() async {
    binding.reportData = report;
    await output?.writeAsString(
      const JsonEncoder.withIndent('  ').convert(report),
      flush: true,
    );
  }

  setUpAll(() async {
    expect(allSourcesEnabled, isTrue);
    expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
    final parent = (await PathProviderPlatform.instance
        .getApplicationSupportPath())!;
    const reuse = String.fromEnvironment('SOURCE_STATE_DIRECTORY');
    state = Directory(
      reuse.isEmpty
          ? '$parent/source-jobs-${DateTime.now().millisecondsSinceEpoch}'
          : reuse,
    );
    await state.create(recursive: true);
    PathProviderPlatform.instance = ValidationPaths(state.path);
    const destination = String.fromEnvironment('SOURCE_JOBS_REPORT');
    output = File(
      destination.isEmpty
          ? '${state.path}/source-jobs-report.json'
          : destination,
    );
    MediaKit.ensureInitialized();
    if (Platform.isWindows) {
      await windowManager.ensureInitialized();
      await windowManager.setSize(const Size(1100, 800));
      await windowManager.center();
      await windowManager.show();
      await windowManager.focus();
    }
    SharedPreferences.setMockInitialValues({});
    store = LocalStore(await SharedPreferences.getInstance());
    await store.disableSourceGate();
    repository = NativeRepository(background: true);
    await repository.initialize();
    await BackgroundDownloads.ensureStarted();
    report['backgroundServicePrepared'] = true;
    await repository.saveResourceSettings(
      const ResourceSettings(
        proxyMode: String.fromEnvironment(
          'SOURCE_PROXY_MODE',
          defaultValue: 'auto',
        ),
        proxyUrl: String.fromEnvironment('SOURCE_PROXY_URL'),
        allowInsecureTLS: bool.fromEnvironment('SOURCE_ALLOW_INSECURE_TLS'),
      ),
    );
    const location = String.fromEnvironment('SOURCE_IMPORT_FILE');
    final uri = Uri.tryParse(location);
    late List<int> bytes;
    if (uri != null &&
        uri.scheme == 'http' &&
        const ['127.0.0.1', '10.0.2.2'].contains(uri.host)) {
      final client = HttpClient()..findProxy = (_) => 'DIRECT';
      try {
        final response = await (await client.getUrl(uri)).close();
        expect(response.statusCode, 200);
        bytes = await response.fold<List<int>>(
          [],
          (all, chunk) => all..addAll(chunk),
        );
      } finally {
        client.close(force: true);
      }
    } else {
      bytes = await File(location).readAsBytes();
    }
    final seed = File('${state.path}/imported-results.json');
    await seed.writeAsBytes(bytes, flush: true);
    report['seedSha256'] = sha256.convert(bytes).toString();
    report['stateDirectory'] = state.path;
    report['import'] = await repository.importSourceResults(seed.path);
    await save();
  });

  final selected = const String.fromEnvironment(
    'SOURCE_IDS',
  ).split(',').where((value) => value.isNotEmpty).toSet();
  final sources = SourceSite.values
      .where((value) => selected.isEmpty || selected.contains(value.id))
      .toList();
  if (const bool.fromEnvironment('WAIT_SOURCE_LOGIN')) {
    sources.sort(
      (a, b) => (a.id == 'sorani' ? 0 : 1).compareTo(b.id == 'sorani' ? 0 : 1),
    );
  }
  for (final source in sources) {
    testWidgets(
      '${source.name} imported update and connection playback check',
      (tester) async {
        final row = <String, dynamic>{
          'source': source.id,
          'name': source.name,
          'status': 'running',
          'operations': <Map<String, dynamic>>[],
        };
        (report['sources'] as List).add(row);
        row['initial'] = jobStatus(await repository.sourceStatus(source.id));
        await tester.pumpWidget(
          MaterialApp(
            home: SourcesScreen(
              key: ValueKey(source.id),
              repository: repository,
              store: store,
              initialSource: source.id,
            ),
          ),
        );
        await tester.pump(const Duration(seconds: 1));
        if (source.id == 'sorani' &&
            const bool.fromEnvironment('WAIT_SOURCE_LOGIN')) {
          final previousInput = binding.shouldPropagateDevicePointerEvents;
          final previousFrames = binding.framePolicy;
          binding.shouldPropagateDevicePointerEvents = true;
          binding.framePolicy =
              LiveTestWidgetsFlutterBindingFramePolicy.fullyLive;
          try {
            final card = find.byKey(const ValueKey('source-sorani'));
            final access = find.descendant(
              of: card,
              matching: find.byKey(const ValueKey('access-sorani')),
            );
            await tester.ensureVisible(access);
            await tester.tap(access);
            await tester.pump(const Duration(seconds: 1));
            await tester.tap(find.byKey(const ValueKey('login-sorani')));
            await tester.pump(const Duration(seconds: 1));
            row['loginWaiting'] = true;
            await save();
            final waiting = Stopwatch()..start();
            while (find.byType(SourceLoginScreen).evaluate().isNotEmpty &&
                waiting.elapsed < const Duration(hours: 1)) {
              await tester.pump(const Duration(seconds: 1));
            }
            expect(find.byType(SourceLoginScreen), findsNothing);
            await tester.pump(const Duration(seconds: 1));
            await tester.tap(find.text('保存并继续'));
            await tester.pump(const Duration(seconds: 2));
            expect(
              (await repository.sourceAccess('sorani'))['configured'],
              true,
            );
            row['loginWaiting'] = false;
            row['loginCaptured'] = true;
            await save();
          } finally {
            binding.shouldPropagateDevicePointerEvents = previousInput;
            binding.framePolicy = previousFrames;
          }
        }
        for (final operation in ['update', 'check']) {
          final entry = <String, dynamic>{
            'operation': operation,
            'history': <Map<String, dynamic>>[],
          };
          (row['operations'] as List).add(entry);
          final started = DateTime.now();
          final button = find.byKey(ValueKey('$operation-${source.id}'));
          await tester.ensureVisible(button);
          await tester.pump(const Duration(seconds: 3));
          await tester.tap(button);
          var observedStart = false;
          String? previous;
          while (true) {
            await tester.pump(const Duration(seconds: 1));
            final status = await repository.sourceStatus(source.id);
            final snapshot = jobStatus(status);
            final signature = jsonEncode(snapshot);
            if (signature != previous) {
              (entry['history'] as List).add({
                'observedAt': DateTime.now().toUtc().toIso8601String(),
                ...snapshot,
              });
              previous = signature;
              await save();
            }
            observedStart =
                observedStart ||
                status.startedAt?.isAfter(
                      started.subtract(const Duration(seconds: 1)),
                    ) ==
                    true;
            if (observedStart && !status.running) {
              entry['final'] = snapshot;
              entry['passed'] =
                  status.error.isEmpty &&
                  status.storageError.isEmpty &&
                  status.stage == '已完成' &&
                  (operation != 'check' ||
                      status.health?.state == 'ok' &&
                          status.health?.steps.length == 5 &&
                          status.health!.steps.every(
                            (step) => step.state == 'ok',
                          ));
              break;
            }
            if (!observedStart &&
                DateTime.now().difference(started) >
                    const Duration(minutes: 2)) {
              entry['passed'] = false;
              entry['error'] = '界面按钮未启动任务';
              break;
            }
          }
          await save();
        }
        var passed = (row['operations'] as List).every(
          (entry) => entry['passed'] == true,
        );
        if (source.id == 'sorani' &&
            const bool.fromEnvironment('WAIT_SOURCE_LOGIN')) {
          final plays = <Map<String, dynamic>>[];
          row['playback'] = plays;
          for (final id in ['4737', '4763', '4787']) {
            final detail = await repository.detail(
              Drama.fromJson({
                'id': 'sorani:$id',
                'source': 'sorani',
                'title': '青空验收',
              }),
            );
            final attempt = <String, dynamic>{
              'id': detail.drama.id,
              'title': detail.drama.title,
              'episode': 1,
              'success': false,
            };
            plays.add(attempt);
            await tester.pumpWidget(
              MaterialApp(
                home: PlayerScreen(
                  key: ValueKey(id),
                  detail: detail,
                  initialIndex: 0,
                  repository: repository,
                  store: store,
                ),
              ),
            );
            final elapsed = Stopwatch()..start();
            int? first, previous;
            var advances = 0;
            VideoController? video;
            while (elapsed.elapsed < const Duration(seconds: 90)) {
              await tester.pump(const Duration(milliseconds: 250));
              final found = find.byType(Video);
              if (found.evaluate().isEmpty) continue;
              video = tester.widget<Video>(found.first).controller;
              final player = video.player.state;
              final position = player.position.inMilliseconds;
              if ((player.width ?? 0) <= 0 || (player.height ?? 0) <= 0) {
                continue;
              }
              first ??= position;
              if (position > (previous ?? position)) advances++;
              previous = position;
              if (position - first >= 5000 && advances >= 3) {
                await tester.runAsync(
                  () => video!.waitUntilFirstFrameRendered.timeout(
                    const Duration(seconds: 15),
                  ),
                );
                attempt.addAll({
                  'success': true,
                  'firstFrameRendered': true,
                  'width': player.width,
                  'height': player.height,
                  'startPositionMs': first,
                  'endPositionMs': position,
                  'advancingSamples': advances,
                });
                break;
              }
            }
            await save();
            await tester.pumpWidget(const SizedBox.shrink());
            await tester.pump(const Duration(seconds: 1));
          }
          row['playbackSuccessCount'] = plays
              .where((entry) => entry['success'] == true)
              .length;
          if (row['playbackSuccessCount'] != 3) passed = false;
        }
        row['status'] = passed ? 'passed' : 'failed';
        await save();
        expect(row['status'], 'passed', reason: '${source.name} 更新或检测未通过，详见报告');
      },
      timeout: const Timeout(Duration(hours: 3)),
    );
  }
  tearDownAll(() async {
    for (final row in report['sources'] as List) {
      if (row['status'] == 'running') {
        row['status'] = row['loginWaiting'] == true
            ? 'pending_login'
            : 'failed';
      }
    }
    report['ok'] =
        (report['sources'] as List).isNotEmpty &&
        (report['sources'] as List).every(
          (entry) => entry['status'] == 'passed',
        );
    report['finishedAt'] = DateTime.now().toUtc().toIso8601String();
    report['export'] = await repository.exportSourceResults(
      '${state.path}/resumed-results.json',
    );
    await save();
    store.dispose();
  });
}
