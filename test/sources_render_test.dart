import 'dart:convert';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

const _huangguoVideo = 'huangguo-video';
const _huangguoKey = PageStorageKey<String>('source-group-huangguo');

const _health = {
  'state': 'failed',
  'checkedAt': '2026-09-19T03:00:00Z',
  'sample': '合成检测剧集',
  'steps': [
    {'name': '入口与目录', 'state': 'ok', 'message': '目录可达', 'httpStatus': 200},
  ],
};

class ScriptedStore extends LocalStore {
  ScriptedStore(
    super.preferences, {
    required super.pinHasher,
    required this.list,
  });

  final List<SourceSite> list;

  @override
  List<SourceSite> get managedSources => list;
}

class StatusFixtureRepository extends FixtureRepository {
  StatusFixtureRepository([Map<String, SourceStatus>? initial])
    : statuses = initial ?? <String, SourceStatus>{};

  final Map<String, SourceStatus> statuses;

  @override
  bool get supportsSourceManagement => true;

  @override
  Future<SourceStatus> sourceStatus(String source) async =>
      statuses[source] ??
      SourceStatus.fromJson({'source': source, 'count': 5, 'page': 2});
}

class UpdateBatchRepository extends FixtureRepository {
  final running = <String, bool>{};
  final failureReasons = <String, String>{};
  final resumables = <String>{};
  final counts = <String, int>{};
  final stages = <String, String>{};
  final operations = <String>[];

  @override
  bool get supportsSourceManagement => true;

  SourceStatus _status(String source) {
    final busy = running[source] ?? false;
    return SourceStatus.fromJson({
      'source': source,
      'count': counts[source] ?? 5,
      'page': 2,
      'hasMore': true,
      'operation': 'update',
      'running': busy,
      'stage': busy ? '补齐剧集资料' : (stages[source] ?? '更新完成'),
      'completed': busy ? 3 : 8,
      'total': 8,
      'resumable': resumables.contains(source),
      'error': busy ? '' : (failureReasons[source] ?? ''),
    });
  }

  @override
  Future<SourceStatus> sourceStatus(String source) async => _status(source);

  @override
  Future<SourceStatus> startSourceJob(
    String source,
    String operation, {
    Drama? drama,
  }) async {
    operations.add('$source:$operation');
    running[source] = true;
    return _status(source);
  }

  @override
  Future<SourceStatus> cancelSourceJob(String source) async {
    operations.add('$source:cancel');
    running[source] = false;
    return _status(source);
  }

  void finish(String source) => running[source] = false;
}

SourceStatus _status({
  String stage = '',
  String error = '',
  bool resumable = false,
  bool running = false,
  int completed = 0,
  int total = 0,
  String storageError = '',
  bool health = false,
  DateTime? retryAt,
}) => SourceStatus.fromJson({
  'source': _huangguoVideo,
  'count': 12,
  'page': 4,
  'hasMore': true,
  'operation': 'update',
  'stage': stage,
  'error': error,
  'resumable': resumable,
  'running': running,
  'completed': completed,
  'total': total,
  'storageError': storageError,
  if (retryAt != null) 'retryAt': retryAt.toUtc().toIso8601String(),
  if (health) 'health': _health,
});

Widget _screen(FixtureRepository repository, LocalStore store, Object tag) =>
    MaterialApp(
      home: KeyedSubtree(
        key: ValueKey(tag),
        child: SourcesScreen(repository: repository, store: store),
      ),
    );

Future<ScriptedStore> _store(
  List<SourceSite> sources, {
  Map<String, Object> preferences = const {},
}) async {
  SharedPreferences.setMockInitialValues(preferences);
  return ScriptedStore(
    await SharedPreferences.getInstance(),
    pinHasher: testPinHasher,
    list: sources,
  );
}

void main() {
  testWidgets(
    'retryable update error inside the 黄果 group renders the card without exception',
    (tester) async {
      tester.view.physicalSize = const Size(1200, 2400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final store = await _store([
        SourceSite.byId(_huangguoVideo),
        SourceSite.byId('huangguoai'),
      ]);
      const error =
          '网络请求失败：站点返回 403，请稍后在浏览器完成验证\n'
          '若仍失败，请重新导入本章会话后再点「接着更新」。';
      final repository = StatusFixtureRepository({
        _huangguoVideo: _status(stage: '网络请求失败', error: error, resumable: true),
        'huangguoai': _status(stage: '更新完成', total: 8, health: true),
      });
      await tester.pumpWidget(_screen(repository, store, 'report'));
      await tester.pump();
      await tester.tap(find.text('黄果'));
      await tester.pump();
      final tile = tester.element(find.byKey(_huangguoKey));
      expect(PageStorage.maybeOf(tile)?.readState(tile), isA<bool>());
      expect(tester.takeException(), isNull);
      expect(
        find.byKey(const ValueKey('source-huangguo-video')),
        findsOneWidget,
      );
      expect(find.text('网络请求失败'), findsOneWidget);
      expect(find.text(error), findsOneWidget);
      expect(find.text('接着更新'), findsOneWidget);
      expect(find.text('尚未检测连接'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );

  testWidgets('source card renders every status combination without exception', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store([
      SourceSite.byId(_huangguoVideo),
      SourceSite.byId('huangguoai'),
    ]);
    final repository = StatusFixtureRepository();
    const stages = ['', '网络请求失败', '多次重试仍未成功'];
    const errors = ['', '合成网络错误', '合成网络错误：站点返回 403，请稍后重试\n如仍失败请重新导入会话。'];
    const resumables = [false, true];
    const runnings = [false, true];
    const progress = [
      [0, 0],
      [3, 8],
      [137, 2001],
    ];
    const storageErrors = ['', '写入目录数据库失败：磁盘空间不足'];
    const healths = [false, true];

    repository.statuses[_huangguoVideo] = _status(stage: '查找新剧');
    repository.statuses['huangguoai'] = _status(stage: '更新完成', total: 8);
    await tester.pumpWidget(_screen(repository, store, 'seed'));
    await tester.pump();
    await tester.tap(find.text('黄果'));
    await tester.pump();
    final tile = tester.element(find.byKey(_huangguoKey));
    expect(PageStorage.maybeOf(tile)?.readState(tile), isA<bool>());

    var index = 0;
    for (final stage in stages) {
      for (final error in errors) {
        for (final resumable in resumables) {
          for (final running in runnings) {
            for (final pair in progress) {
              for (final storageError in storageErrors) {
                for (final health in healths) {
                  index++;
                  repository.statuses[_huangguoVideo] = _status(
                    stage: stage,
                    error: error,
                    resumable: resumable,
                    running: running,
                    completed: pair[0],
                    total: pair[1],
                    storageError: storageError,
                    health: health,
                  );
                  await tester.pumpWidget(
                    _screen(repository, store, 'matrix-$index'),
                  );
                  await tester.pump();
                  await tester.pump();
                  expect(
                    tester.takeException(),
                    isNull,
                    reason:
                        'stage=$stage error=${error.length} '
                        'resumable=$resumable running=$running '
                        'completed=${pair[0]}/${pair[1]} '
                        'storageError=${storageError.isNotEmpty} health=$health',
                  );
                  expect(
                    find.byKey(const ValueKey('source-huangguo-video')),
                    findsOneWidget,
                    reason: 'status=$index 站源卡片未渲染',
                  );
                  if (error.isNotEmpty) {
                    expect(find.text(error), findsOneWidget);
                  }
                  if (stage.isNotEmpty && !running) {
                    expect(find.text(stage), findsOneWidget);
                  }
                }
              }
            }
          }
        }
      }
    }
    expect(index, 432);
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
  });

  testWidgets('source card renders retry countdown combinations', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store([
      SourceSite.byId(_huangguoVideo),
      SourceSite.byId('huangguoai'),
    ]);
    final repository = StatusFixtureRepository();
    final retries = <DateTime?>[
      null,
      DateTime.now().add(const Duration(seconds: 30)),
    ];
    const errors = ['', '合成网络错误', '合成网络错误：站点返回 403\n请稍后重试。'];
    const storageErrors = ['', '写入目录数据库失败'];
    const healths = [false, true];

    repository.statuses[_huangguoVideo] = _status(stage: '查找新剧');
    repository.statuses['huangguoai'] = _status(stage: '更新完成', total: 8);
    await tester.pumpWidget(_screen(repository, store, 'retry-seed'));
    await tester.pump();
    await tester.tap(find.text('黄果'));
    await tester.pump();

    var index = 0;
    for (final retryAt in retries) {
      for (final error in errors) {
        for (final storageError in storageErrors) {
          for (final health in healths) {
            index++;
            repository.statuses[_huangguoVideo] = _status(
              stage: '网络请求失败',
              error: error,
              resumable: true,
              storageError: storageError,
              health: health,
              retryAt: retryAt,
            );
            await tester.pumpWidget(_screen(repository, store, 'retry-$index'));
            await tester.pump();
            await tester.pump();
            expect(
              tester.takeException(),
              isNull,
              reason:
                  'retry=${retryAt != null} error=${error.length} '
                  'storageError=${storageError.isNotEmpty} health=$health',
            );
            if (retryAt != null) {
              expect(find.textContaining('秒后重试'), findsOneWidget);
            } else {
              expect(find.textContaining('秒后重试'), findsNothing);
            }
          }
        }
      }
    }
    expect(index, 24);
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
  });

  test('progress label spells out processed and remaining episodes', () {
    expect(
      sourceProgressText(_status(stage: '补齐剧集资料', completed: 137, total: 2245)),
      '补齐剧集资料 · 已处理 137/2245 部 · 剩余 2108 部',
    );
    expect(
      sourceProgressText(
        _status(stage: '补齐剧集资料', completed: 2245, total: 2245),
      ),
      '补齐剧集资料 · 已处理 2245/2245 部',
    );
    expect(sourceProgressText(_status(stage: '查找新剧')), '查找新剧');
    expect(sourceProgressText(_status()), '准备中');
    expect(sourceProgressText(null), '准备中');
  });

  testWidgets('update all starts every stale source in one concurrent pass', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store(
      const [
        SourceSite.hongguo,
        SourceSite.guipian,
        SourceSite.sorani,
        SourceSite.hanxiaoquan,
      ],
      preferences: {
        'disabledSources': jsonEncode(['hongguo']),
      },
    );
    final repository = UpdateBatchRepository()
      ..counts['sorani'] = 0
      ..stages['sorani'] = '';
    final started = [
      for (final source in store.managedSources)
        if (store.sourceEnabled(source.id)) '${source.id}:update',
    ];
    expect(started.length, 3);
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('一键更新'), findsOneWidget);
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, started);
    expect(find.text('一键更新'), findsOneWidget);
    expect(find.text('停止一键更新'), findsNothing);
    expect(find.text('一键更新：本会话 3 个源 · 已完成 0 · 失败 0'), findsOneWidget);
    await _finishRun(tester, repository, started);
    expect(find.textContaining('一键更新完成：成功 3 个、失败 0 个、跳过 0 个'), findsOneWidget);
    expect(find.textContaining('一键更新：本会话'), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 300));
    store.dispose();
  });

  testWidgets('update all relaunches only the failed source on a new click', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store(const [
      SourceSite.hongguo,
      SourceSite.guipian,
      SourceSite.sorani,
    ]);
    final repository = UpdateBatchRepository();
    final started = [
      for (final source in store.managedSources)
        if (store.sourceEnabled(source.id)) '${source.id}:update',
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, started);
    repository.failureReasons['sorani'] = '站点验证失败';
    repository.finish('sorani');
    await tester.pump(const Duration(milliseconds: 300));
    await tester.pump();
    expect(find.text('一键更新：本会话 3 个源 · 已完成 0 · 失败 1'), findsOneWidget);
    repository.failureReasons.remove('sorani');
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, [...started, 'sorani:update']);
    expect(find.text('一键更新：本会话 4 个源 · 已完成 0 · 失败 1'), findsOneWidget);
    await _finishRun(tester, repository, started);
    expect(find.textContaining('一键更新完成：成功 3 个、失败 1 个、跳过 0 个'), findsOneWidget);
    expect(find.textContaining('青空：站点验证失败'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 300));
    store.dispose();
  });

  testWidgets('batch update button fits a narrow admin app bar', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store(const [
      SourceSite.hongguo,
      SourceSite.guipian,
      SourceSite.sorani,
    ]);
    final repository = UpdateBatchRepository();
    final started = [
      for (final source in store.managedSources)
        if (store.sourceEnabled(source.id)) '${source.id}:update',
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(find.text('一键更新'), findsOneWidget);
    expect(find.text('停止一键更新'), findsNothing);
    expect(find.textContaining('一键更新：本会话 3 个源'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await _finishRun(tester, repository, started);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 300));
    store.dispose();
  });

  testWidgets('update all skips a source that is already updating manually', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store(const [
      SourceSite.hongguo,
      SourceSite.guipian,
      SourceSite.sorani,
    ]);
    final repository = UpdateBatchRepository();
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('update-hongguo')));
    await tester.pump();
    expect(repository.operations, ['hongguo:update']);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const ValueKey('update-hongguo')))
          .onPressed,
      isNull,
    );
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, [
      'hongguo:update',
      'guipian:update',
      'sorani:update',
    ]);
    expect(find.text('一键更新：本会话 2 个源 · 已完成 0 · 失败 0'), findsOneWidget);
    await _finishRun(tester, repository, const [
      'guipian:update',
      'sorani:update',
    ]);
    expect(find.textContaining('一键更新完成：成功 2 个、失败 0 个、跳过 0 个'), findsOneWidget);
    repository.finish('hongguo');
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    await tester.tap(find.text('一键更新'));
    await tester.pumpAndSettle();
    expect(find.text('所有启用站源均已更新完成'), findsOneWidget);
    expect(repository.operations, [
      'hongguo:update',
      'guipian:update',
      'sorani:update',
    ]);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 300));
    store.dispose();
  });

  testWidgets(
    'a second click mid-run does not reset or duplicate the summary',
    (tester) async {
      tester.view.physicalSize = const Size(1200, 2400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final store = await _store(const [
        SourceSite.hongguo,
        SourceSite.guipian,
        SourceSite.sorani,
      ]);
      final repository = UpdateBatchRepository();
      final started = [
        for (final source in store.managedSources)
          if (store.sourceEnabled(source.id)) '${source.id}:update',
      ];
      await tester.pumpWidget(
        MaterialApp(
          home: SourcesScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('一键更新'));
      await tester.pump();
      expect(repository.operations, started);
      repository.finish('guipian');
      repository.finish('sorani');
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump();
      expect(find.text('一键更新：本会话 3 个源 · 已完成 2 · 失败 0'), findsOneWidget);
      await tester.tap(find.text('一键更新'));
      await tester.pump();
      expect(repository.operations, started);
      expect(find.textContaining('一键更新完成'), findsNothing);
      expect(find.text('一键更新：本会话 3 个源 · 已完成 2 · 失败 0'), findsOneWidget);
      await _finishRun(tester, repository, started);
      expect(
        find.textContaining('一键更新完成：成功 3 个、失败 0 个、跳过 0 个'),
        findsOneWidget,
      );
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump(const Duration(milliseconds: 300));
      store.dispose();
    },
  );

  testWidgets(
    'update all resumes a failed resumable source on the next click',
    (tester) async {
      tester.view.physicalSize = const Size(1200, 2400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final store = await _store(const [
        SourceSite.hongguo,
        SourceSite.guipian,
        SourceSite.sorani,
      ]);
      final repository = UpdateBatchRepository()
        ..failureReasons['sorani'] = '站点验证失败';
      final started = [
        for (final source in store.managedSources)
          if (store.sourceEnabled(source.id)) '${source.id}:update',
      ];
      await tester.pumpWidget(
        MaterialApp(
          home: SourcesScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('一键更新'));
      await tester.pump();
      expect(repository.operations, started);
      await _finishRun(tester, repository, started);
      expect(repository.operations, started);
      repository.failureReasons.remove('sorani');
      repository.resumables.add('sorani');
      await tester.pump(const Duration(seconds: 10));
      await tester.pump();
      await tester.pump();
      await tester.pump(const Duration(seconds: 5));
      await tester.pumpAndSettle();
      await tester.tap(find.text('一键更新'));
      await tester.pump();
      expect(repository.operations, [...started, 'sorani:resume']);
      await _finishRun(tester, repository, const ['sorani:resume']);
      expect(
        find.textContaining('一键更新完成：成功 1 个、失败 0 个、跳过 2 个'),
        findsOneWidget,
      );
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump(const Duration(milliseconds: 300));
      store.dispose();
    },
  );

  testWidgets('update all offers a forced rerun once every source succeeded', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store(const [
      SourceSite.hongguo,
      SourceSite.guipian,
      SourceSite.sorani,
    ]);
    final repository = UpdateBatchRepository();
    final started = [
      for (final source in store.managedSources)
        if (store.sourceEnabled(source.id)) '${source.id}:update',
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, started);
    await _finishRun(tester, repository, started);
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    await tester.tap(find.text('一键更新'));
    await tester.pumpAndSettle();
    expect(repository.operations, started);
    expect(find.text('所有启用站源均已更新完成'), findsOneWidget);
    expect(find.text('强制全部重新更新'), findsOneWidget);
    await tester.tap(find.text('强制全部重新更新'));
    await tester.pump();
    expect(repository.operations, [...started, ...started]);
    expect(find.text('一键更新：本会话 3 个源 · 已完成 0 · 失败 0'), findsOneWidget);
    await _finishRun(tester, repository, started);
    expect(find.textContaining('一键更新完成：成功 3 个、失败 0 个、跳过 0 个'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 300));
    store.dispose();
  });

  testWidgets('update all counts only idle clean sources as skipped', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final store = await _store(const [
      SourceSite.hongguo,
      SourceSite.guipian,
      SourceSite.sorani,
    ]);
    final repository = UpdateBatchRepository();
    final started = [
      for (final source in store.managedSources)
        if (store.sourceEnabled(source.id)) '${source.id}:update',
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, started);
    await _finishRun(tester, repository, started);
    expect(find.textContaining('一键更新完成：成功 3 个、失败 0 个、跳过 0 个'), findsOneWidget);
    repository.failureReasons['sorani'] = '站点验证失败';
    await tester.pump(const Duration(seconds: 10));
    await tester.pump();
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    await tester.tap(find.text('一键更新'));
    await tester.pump();
    expect(repository.operations, [...started, 'sorani:update']);
    expect(find.text('一键更新：本会话 1 个源 · 已完成 0 · 失败 0'), findsOneWidget);
    await _finishRun(tester, repository, const ['sorani:update']);
    expect(find.textContaining('一键更新完成：成功 0 个、失败 1 个、跳过 2 个'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 300));
    store.dispose();
  });
}

Future<void> _finishRun(
  WidgetTester tester,
  UpdateBatchRepository repository,
  List<String> started,
) async {
  for (final operation in started) {
    repository.finish(operation.split(':').first);
  }
  for (var index = 0; index < 4; index++) {
    await tester.pump(const Duration(milliseconds: 300));
    await tester.pump();
  }
}
