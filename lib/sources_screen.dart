import 'dart:async';
import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as p;

import 'core_bridge.dart';
import 'local_store.dart';
import 'models.dart';
import 'source_status.dart';

String sourceResultStamp(DateTime value) {
  String two(int number) => number.toString().padLeft(2, '0');
  return '${value.year}${two(value.month)}${two(value.day)}'
      '${two(value.hour)}${two(value.minute)}${two(value.second)}';
}

String sourceResultSize(int bytes) {
  if (bytes < 1024) return '$bytes B';
  if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
  return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
}

String sourceTimestamp(DateTime? value) {
  if (value == null) return '尚无记录';
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.month}/${local.day} ${two(local.hour)}:${two(local.minute)}';
}

String sourceProgressText(SourceStatus? status) {
  final stage = status?.stage ?? '';
  final label = stage.isEmpty ? '准备中' : stage;
  final total = status?.total ?? 0;
  if (status == null || total <= 0) return label;
  final completed = status.completed;
  final remaining = total - completed;
  return '$label · 已处理 $completed/$total 部'
      '${remaining > 0 ? ' · 剩余 $remaining 部' : ''}';
}

enum _BatchOutcome { succeeded, failed, skipped, notReached }

class SourcesScreen extends StatefulWidget {
  const SourcesScreen({
    super.key,
    required this.repository,
    required this.store,
    this.initialSource,
    this.drama,
  });

  final AppRepository repository;
  final LocalStore store;
  final String? initialSource;
  final Drama? drama;

  @override
  State<SourcesScreen> createState() => _SourcesScreenState();
}

class _SourcesScreenState extends State<SourcesScreen> {
  final _statuses = <String, SourceStatus>{};
  final _errors = <String, String>{};
  final _pending = <String>{};
  final _revisions = <String, int>{};
  final _expandedHealth = <String>{};
  final _batchOutcomes = <String, _BatchOutcome>{};
  final _batchActive = <String>{};
  final _batchNames = <String, String>{};
  final _batchFailures = <String>[];
  final _manualUpdateIds = <String>{};
  final _cardStorage = PageStorageBucket();
  Timer? _timer;
  bool _polling = false;
  bool _transferring = false;
  int _batchRun = 0;
  int _batchLaunched = 0;
  int _batchSucceeded = 0;
  int _batchFailed = 0;
  int _batchSkipped = 0;
  int _ticks = 0;

  bool get _batchTracking => _batchActive.isNotEmpty;

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      _ticks++;
      if (_statuses.values.any((status) => status.retryAt != null)) {
        setState(() {});
      }
      if (_ticks % 2 == 0 &&
          (_statuses.values.any((status) => status.running) ||
              _ticks % 10 == 0)) {
        unawaited(_refresh());
      }
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_polling) return;
    _polling = true;
    final epoch = widget.store.profileEpoch;
    try {
      await Future.wait([
        for (final source in widget.store.managedSources)
          (() async {
            final revision = _revisions[source.id] ?? 0;
            try {
              final status = await widget.repository.sourceStatus(source.id);
              if (!mounted ||
                  epoch != widget.store.profileEpoch ||
                  revision != (_revisions[source.id] ?? 0)) {
                return;
              }
              setState(() {
                _statuses[source.id] = status;
                _errors.remove(source.id);
                _markManualOutcome(source, status);
              });
            } catch (error) {
              if (mounted &&
                  epoch == widget.store.profileEpoch &&
                  revision == (_revisions[source.id] ?? 0)) {
                setState(() => _errors[source.id] = error.toString());
              }
            }
          })(),
      ]);
    } finally {
      _polling = false;
    }
    _collectBatchFinished();
  }

  Future<void> _run(
    SourceSite source,
    String operation, {
    bool manual = false,
  }) async {
    if (_pending.contains(source.id)) return;
    final epoch = widget.store.profileEpoch;
    if (manual && (operation == 'update' || operation == 'resume')) {
      _manualUpdateIds.add(source.id);
    }
    setState(() {
      _pending.add(source.id);
      _errors.remove(source.id);
      _revisions[source.id] = (_revisions[source.id] ?? 0) + 1;
      if (operation == 'check' || operation == 'checkCatalog') {
        _expandedHealth.add(source.id);
      }
    });
    try {
      final status = operation == 'cancel'
          ? await widget.repository.cancelSourceJob(source.id)
          : await widget.repository.startSourceJob(
              source.id,
              operation,
              drama: source.id == widget.drama?.source ? widget.drama : null,
            );
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() {
          _statuses[source.id] = status;
          if (operation == 'cancel') {
            _manualUpdateIds.remove(source.id);
          } else {
            _markManualOutcome(source, status);
          }
        });
      }
    } catch (error) {
      if (mounted && epoch == widget.store.profileEpoch) {
        setState(() => _errors[source.id] = error.toString());
      }
    } finally {
      if (mounted) setState(() => _pending.remove(source.id));
    }
  }

  void _markManualOutcome(SourceSite source, SourceStatus status) {
    if (!_manualUpdateIds.contains(source.id) ||
        _pending.contains(source.id) ||
        status.running) {
      return;
    }
    _manualUpdateIds.remove(source.id);
    _batchOutcomes[source.id] = status.error.isEmpty
        ? _BatchOutcome.succeeded
        : _BatchOutcome.failed;
  }

  void _toggleUpdateAll() => unawaited(_updateAllSources());

  bool _batchPending(SourceSite source) {
    final outcome = _batchOutcomes[source.id];
    if (outcome == null ||
        outcome == _BatchOutcome.failed ||
        outcome == _BatchOutcome.notReached) {
      return true;
    }
    final error = _errors[source.id] ?? _statuses[source.id]?.error ?? '';
    if (error.isNotEmpty) return true;
    return _statuses[source.id]?.resumable == true;
  }

  String _batchOperation(SourceSite source) =>
      _statuses[source.id]?.resumable == true ? 'resume' : 'update';

  void _showAllUpdatedMessage() {
    final messenger = ScaffoldMessenger.of(context);
    messenger.showSnackBar(
      SnackBar(
        content: const Text('所有启用站源均已更新完成'),
        action: SnackBarAction(
          label: '强制全部重新更新',
          onPressed: () => unawaited(_updateAllSources(force: true)),
        ),
      ),
    );
  }

  Future<void> _updateAllSources({bool force = false}) async {
    final epoch = widget.store.profileEpoch;
    final enabled = [
      for (final source in widget.store.managedSources)
        if (widget.store.sourceEnabled(source.id)) source,
    ];
    if (enabled.isEmpty) {
      _showMessage('没有启用的站源');
      return;
    }
    final idle = [
      for (final source in enabled)
        if (!_pending.contains(source.id) &&
            _statuses[source.id]?.running != true)
          source,
    ];
    final sources = force
        ? idle
        : [
            for (final source in idle)
              if (_batchPending(source)) source,
          ];
    if (sources.isEmpty) {
      if (_batchActive.isEmpty) _showAllUpdatedMessage();
      return;
    }
    final run = ++_batchRun;
    setState(() {
      if (_batchActive.isEmpty) {
        _batchLaunched = 0;
        _batchSucceeded = 0;
        _batchFailed = 0;
        _batchFailures.clear();
      }
      _batchSkipped = idle.length - sources.length;
      _batchLaunched += sources.length;
      final launching = {for (final source in sources) source.id};
      for (final source in idle) {
        if (!launching.contains(source.id)) {
          _batchOutcomes[source.id] = _BatchOutcome.skipped;
        }
      }
      for (final source in sources) {
        _batchActive.add(source.id);
        _batchNames[source.id] = source.name;
      }
    });
    for (final source in sources) {
      unawaited(_run(source, _batchOperation(source)));
    }
    await _awaitBatchFinished(run, epoch);
  }

  Future<void> _awaitBatchFinished(int run, int epoch) async {
    while (mounted &&
        run == _batchRun &&
        epoch == widget.store.profileEpoch &&
        _batchActive.isNotEmpty) {
      await _refresh();
      if (_batchActive.isEmpty) break;
      await Future<void>.delayed(const Duration(milliseconds: 250));
    }
    if (!mounted || run != _batchRun) return;
    if (epoch != widget.store.profileEpoch) {
      setState(_clearBatchSession);
      return;
    }
    if (_batchActive.isNotEmpty) return;
    _finishBatchSession();
  }

  void _collectBatchFinished() {
    if (_batchActive.isEmpty) return;
    final finished = <String>[];
    for (final id in _batchActive) {
      if (_pending.contains(id) || _statuses[id]?.running == true) continue;
      finished.add(id);
    }
    if (finished.isEmpty) return;
    for (final id in finished) {
      _batchActive.remove(id);
      final reason = _errors[id] ?? _statuses[id]?.error ?? '';
      if (reason.isEmpty) {
        _batchSucceeded++;
        _batchOutcomes[id] = _BatchOutcome.succeeded;
      } else {
        _batchFailed++;
        _batchOutcomes[id] = _BatchOutcome.failed;
        _batchFailures.add('${_batchNames[id] ?? id}：$reason');
      }
    }
    if (mounted) setState(() {});
  }

  void _clearBatchSession() {
    _batchActive.clear();
    _batchNames.clear();
    _batchLaunched = 0;
    _batchSucceeded = 0;
    _batchFailed = 0;
    _batchSkipped = 0;
    _batchFailures.clear();
  }

  void _finishBatchSession() {
    final succeeded = _batchSucceeded;
    final failed = _batchFailed;
    final skipped = _batchSkipped;
    final failures = List<String>.from(_batchFailures);
    _clearBatchSession();
    if (mounted) setState(() {});
    final summary =
        '一键更新完成：成功 $succeeded 个、失败 $failed 个、'
        '跳过 $skipped 个';
    _showMessage(
      failures.isEmpty ? summary : '$summary\n${failures.join('\n')}',
    );
  }

  Future<void> _copy(SourceSite source, SourceStatus status) async {
    final text = StringBuffer('${source.name}\n');
    text.writeln(
      '缓存 ${status.count} 部，更新 ${sourceTimestamp(status.updatedAt)}',
    );
    final health = status.health;
    if (health != null) {
      text.writeln('${health.label} · ${sourceTimestamp(health.checkedAt)}');
      if (health.sample.isNotEmpty) text.writeln('检测剧集：${health.sample}');
      for (final step in health.steps) {
        text.writeln('${step.name}：${step.message}');
        text.writeln(
          '${step.host} HTTP ${step.httpStatus} · ${step.elapsedMs} ms',
        );
        if (step.cfRay.isNotEmpty) text.writeln('CF Ray: ${step.cfRay}');
      }
    }
    if (status.error.isNotEmpty) text.writeln(status.error);
    if (status.storageError.isNotEmpty) text.writeln(status.storageError);
    await Clipboard.setData(ClipboardData(text: text.toString()));
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('诊断信息已复制')));
    }
  }

  Future<void> _configure(
    SourceSite source, {
    bool? removed,
    bool? enabled,
    String? name,
  }) async {
    try {
      if (removed == true || enabled == false) {
        await _cancelSource(source);
      }
      await widget.store.configureSource(
        source.id,
        removed: removed,
        enabled: enabled,
        name: name,
      );
      if (mounted) setState(() {});
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    }
  }

  Future<void> _restoreSource(String id) =>
      _configure(SourceSite.byId(id), removed: false, enabled: true);

  Future<void> _cancelSource(SourceSite source) async {
    final wasRunning =
        _pending.contains(source.id) || _statuses[source.id]?.running == true;
    try {
      final status = await widget.repository.cancelSourceJob(source.id);
      if (mounted) setState(() => _statuses[source.id] = status);
    } catch (error) {
      if (mounted && wasRunning) _showMessage(error.toString());
    }
  }

  void _showMessage(String message) {
    final messenger = ScaffoldMessenger.of(context);
    messenger.showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _exportSourceResults() async {
    if (_transferring) return;
    final directory = await FilePicker.getDirectoryPath(
      dialogTitle: '选择站源结果保存目录',
    );
    if (directory == null || directory.isEmpty || !mounted) return;
    final file = p.join(
      directory,
      'zhenguojian-source-results-${sourceResultStamp(DateTime.now())}.json',
    );
    setState(() => _transferring = true);
    try {
      final result = await widget.repository.exportSourceResults(file);
      if (!mounted) return;
      _showMessage(
        '已导出 ${intValue(result['sources'])} 个站源、${intValue(result['items'])} 条目录到 '
        '${result['path'] ?? file}（${sourceResultSize(intValue(result['bytes']))}）',
      );
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    } finally {
      if (mounted) setState(() => _transferring = false);
    }
  }

  Future<void> _importSourceResults() async {
    if (_transferring) return;
    final file = await FilePicker.pickFile(
      type: FileType.custom,
      allowedExtensions: ['json'],
      dialogTitle: '选择站源结果文件',
    );
    final filePath = file?.path;
    if (file == null || filePath == null || filePath.isEmpty || !mounted) {
      return;
    }
    final accepted = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('导入站源结果？'),
        content: const Text(
          '将把文件中的站源目录与更新进度合并到本机：同名的剧集补齐资料，本机分页进度更靠前时保留本机进度。'
          '不会替换其它站源、下载任务和追剧记录，也不会导入站点会话凭据。'
          '导入后不会自动更新，请在卡片上点「更新」或「接着更新」继续。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('导入并合并'),
          ),
        ],
      ),
    );
    if (accepted != true || !mounted) return;
    setState(() => _transferring = true);
    try {
      final result = await widget.repository.importSourceResults(filePath);
      await _refresh();
      if (!mounted) return;
      final details = (result['details'] as List? ?? [])
          .whereType<Map>()
          .map((row) {
            final id = row['source'] as String? ?? '';
            final name = SourceSite.isAvailable(id)
                ? SourceSite.byId(id).name
                : id;
            return '$name ${intValue(row['items'])} 条';
          })
          .join('、');
      final skipped = (result['skippedSources'] as List? ?? [])
          .whereType<String>()
          .toList();
      final skippedItems = intValue(result['skippedItems']);
      _showMessage(
        '已导入 ${intValue(result['sources'])} 个站源、${intValue(result['items'])} 条目录'
        '${details.isEmpty ? '' : '（$details）'}'
        '${skippedItems > 0 ? '，忽略 $skippedItems 条无效条目' : ''}'
        '${skipped.isEmpty ? '' : '；${skipped.join('、')} 在当前版本不可用，已跳过'}',
      );
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    } finally {
      if (mounted) setState(() => _transferring = false);
    }
  }

  Future<void> _rename(SourceSite source) async {
    final controller = TextEditingController(text: source.name);
    final name = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('编辑${source.name}名称'),
        content: TextField(
          controller: controller,
          maxLength: 30,
          autofocus: true,
          decoration: const InputDecoration(labelText: '显示名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, controller.text),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (name != null && mounted) await _configure(source, name: name);
  }

  Future<void> _sourceAccess(SourceSite source) async {
    final origin = TextEditingController();
    final cookie = TextEditingController();
    final userAgent = TextEditingController();
    final token = TextEditingController();
    final deviceId = TextEditingController();
    try {
      final info = await widget.repository.sourceAccess(source.id);
      if (!mounted) return;
      final url = info['url'] as String? ?? '';
      origin.text = info['origin'] as String? ?? url;
      final saved = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('${source.name}验证与授权'),
          content: SizedBox(
            width: 560,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text('站点地址：$url'),
                  const SizedBox(height: 8),
                  const Text('仅导入本人已完成验证或已获授权的站点会话。会话只在本次运行内有效，关闭应用后需重新导入。'),
                  TextField(
                    controller: origin,
                    decoration: const InputDecoration(
                      labelText: '验证站点 HTTPS 地址',
                    ),
                  ),
                  TextField(
                    controller: cookie,
                    maxLines: 2,
                    decoration: const InputDecoration(labelText: '同域 Cookie'),
                  ),
                  TextField(
                    controller: userAgent,
                    decoration: const InputDecoration(
                      labelText: '浏览器 User-Agent（可选）',
                    ),
                  ),
                  if (source.id == 'huangdou') ...[
                    TextField(
                      controller: token,
                      decoration: const InputDecoration(
                        labelText: '本人站点授权令牌（可选）',
                      ),
                    ),
                    TextField(
                      controller: deviceId,
                      decoration: const InputDecoration(
                        labelText: '该令牌对应设备标识（可选）',
                      ),
                    ),
                  ],
                  if (Platform.isAndroid)
                    TextButton.icon(
                      onPressed: () async {
                        try {
                          final result = await _verifySource(origin.text);
                          if (result == null || !context.mounted) return;
                          origin.text = result['origin'] ?? origin.text;
                          cookie.text = result['cookie'] ?? '';
                          userAgent.text = result['userAgent'] ?? '';
                        } catch (error) {
                          if (context.mounted) _showMessage(error.toString());
                        }
                      },
                      icon: const Icon(Icons.open_in_browser),
                      label: const Text('打开人工验证页'),
                    ),
                  if (Platform.isWindows)
                    TextButton.icon(
                      onPressed: () => _openInBrowser(origin.text),
                      icon: const Icon(Icons.open_in_browser),
                      label: const Text('在浏览器打开，验证后粘贴同域 Cookie 与 UA'),
                    ),
                  const Padding(
                    padding: EdgeInsets.only(top: 8),
                    child: Text(
                      'Android 可在应用内完成站点验证并自动读取同域 Cookie；Windows 与 iOS 请在浏览器完成验证后，手工粘贴同域 Cookie 与 User-Agent。',
                    ),
                  ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('取消'),
            ),
            if (info['configured'] == true)
              TextButton(
                onPressed: () => _clearAccess(context, source),
                child: const Text('清除本次会话'),
              ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('保存并继续'),
            ),
          ],
        ),
      );
      if (saved == true) {
        await _saveSourceAccess(
          source,
          origin: origin,
          cookie: cookie,
          userAgent: userAgent,
          token: token,
          deviceId: deviceId,
        );
      }
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    } finally {
      origin.dispose();
      cookie.dispose();
      userAgent.dispose();
      token.dispose();
      deviceId.dispose();
    }
  }

  Future<Map<String, String>?> _verifySource(String url) => const MethodChannel(
    'duanju/device',
  ).invokeMapMethod<String, String>('verifySource', {'url': url.trim()});

  Future<void> _openInBrowser(String url) async {
    final address = url.trim();
    if (address.isEmpty) {
      _showMessage('请先填写验证站点 HTTPS 地址');
      return;
    }
    try {
      await Process.start('explorer.exe', [address]);
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    }
  }

  Future<void> _saveSourceAccess(
    SourceSite source, {
    required TextEditingController origin,
    required TextEditingController cookie,
    required TextEditingController userAgent,
    required TextEditingController token,
    required TextEditingController deviceId,
  }) async {
    await widget.repository.saveSourceAccess(
      source.id,
      origin: origin.text.trim(),
      cookie: cookie.text.trim(),
      userAgent: userAgent.text.trim(),
      accessToken: token.text.trim(),
      deviceId: deviceId.text.trim(),
    );
    if (mounted) await _resumeAfterAccess(source);
  }

  Future<void> _resumeAfterAccess(SourceSite source) async {
    if (!widget.store.sourceEnabled(source.id)) return;
    SourceStatus? status = _statuses[source.id];
    try {
      status = await widget.repository.sourceStatus(source.id);
    } catch (_) {}
    if (status == null || status.running) return;
    if (!_resumableJob(status)) return;
    await _run(source, 'resume', manual: true);
  }

  bool _resumableJob(SourceStatus status) =>
      status.resumable ||
      status.operation == 'update' && status.error.isNotEmpty;

  Future<void> _clearAccess(BuildContext context, SourceSite source) async {
    try {
      await widget.repository.clearSourceAccess(source.id);
      if (context.mounted) Navigator.pop(context, false);
    } catch (error) {
      if (context.mounted) _showMessage(error.toString());
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.store,
    builder: (context, _) {
      final sources = widget.store.managedSources.toList()
        ..sort((a, b) {
          final aFirst = a.id == widget.initialSource ? 0 : 1;
          final bFirst = b.id == widget.initialSource ? 0 : 1;
          return aFirst.compareTo(bFirst);
        });
      final viewPaddingBottom = MediaQuery.viewPaddingOf(context).bottom;
      final paddingBottom = MediaQuery.paddingOf(context).bottom;
      final bottomInset = viewPaddingBottom > paddingBottom
          ? viewPaddingBottom
          : paddingBottom;
      return Scaffold(
        appBar: AppBar(
          title: const Text('站源管理'),
          actions: [
            if (widget.store.profile.admin) ...[
              PopupMenuButton<String>(
                tooltip: '站源结果与内置站源',
                icon: const Icon(Icons.more_vert),
                onSelected: (value) {
                  if (value == 'export') {
                    unawaited(_exportSourceResults());
                  } else if (value == 'import') {
                    unawaited(_importSourceResults());
                  } else if (value.startsWith('restore:')) {
                    unawaited(_restoreSource(value.substring(8)));
                  }
                },
                itemBuilder: (_) => [
                  PopupMenuItem(
                    value: 'export',
                    enabled: !_transferring,
                    child: const Text('导出站源结果'),
                  ),
                  PopupMenuItem(
                    value: 'import',
                    enabled: !_transferring,
                    child: const Text('导入站源结果'),
                  ),
                  const PopupMenuDivider(),
                  for (final source in widget.store.removedSources)
                    PopupMenuItem(
                      value: 'restore:${source.id}',
                      child: Text('恢复 ${source.name}'),
                    ),
                  if (widget.store.removedSources.isEmpty)
                    const PopupMenuItem(
                      enabled: false,
                      child: Text('所有内置站源已添加'),
                    ),
                ],
              ),
              TextButton.icon(
                onPressed: _toggleUpdateAll,
                icon: const Icon(Icons.bolt_rounded),
                label: const Text('一键更新'),
              ),
            ],
          ],
        ),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 960),
            child: RefreshIndicator(
              onRefresh: _refresh,
              child: ListView(
                padding: EdgeInsets.fromLTRB(16, 16, 16, 16 + bottomInset),
                children: [
                  const Padding(
                    padding: EdgeInsets.only(bottom: 16),
                    child: Text(
                      '添加可恢复当前版本内置解析器；可编辑显示名称、启停或移除。更新会持续推进目录和资料，离开此页后继续运行。',
                    ),
                  ),
                  if (_batchTracking)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 16),
                      child: Text(
                        '一键更新：本会话 $_batchLaunched 个源 · 已完成 $_batchSucceeded · 失败 $_batchFailed',
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.primary,
                        ),
                      ),
                    ),
                  if (sources.isEmpty)
                    const Padding(
                      padding: EdgeInsets.all(24),
                      child: Text('当前用户没有可用站源'),
                    ),
                  for (final group in SourceGroup.fromSources(sources))
                    if (group.id == 'huangguo')
                      Card(
                        clipBehavior: Clip.antiAlias,
                        child: ExpansionTile(
                          key: const PageStorageKey('source-group-huangguo'),
                          initiallyExpanded: group.sources.any(
                            (source) => source.id == widget.initialSource,
                          ),
                          leading: const Icon(Icons.hub_outlined),
                          title: const Text('黄果'),
                          subtitle: Text(
                            '${group.sources.length} 个入口 · ${group.sources.fold<int>(0, (count, source) => count + (_statuses[source.id]?.count ?? 0))} 部',
                          ),
                          childrenPadding: const EdgeInsets.all(8),
                          children: [
                            for (final source in group.sources)
                              PageStorage(
                                bucket: _cardStorage,
                                child: _sourceCard(source),
                              ),
                          ],
                        ),
                      )
                    else
                      for (final source in group.sources) _sourceCard(source),
                ],
              ),
            ),
          ),
        ),
      );
    },
  );

  Widget _sourceCard(SourceSite source) {
    final status = _statuses[source.id];
    final pending = _pending.contains(source.id);
    final busy = pending || status?.running == true;
    final active = widget.store.sourceEnabled(source.id);
    final seconds = status?.retrySeconds ?? 0;
    final manageable = widget.repository.supportsSourceManagement;
    final enabled = active && !busy && seconds == 0 && manageable;
    final error = _errors[source.id] ?? status?.error ?? '';
    final health = status?.health;
    final healthExpanded = _expandedHealth.contains(source.id);
    final colors = Theme.of(context).colorScheme;
    return Card(
      key: ValueKey('source-${source.id}'),
      margin: const EdgeInsets.only(bottom: 16),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.dns_outlined),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    source.name,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                Text('${status?.count ?? 0} 部'),
                if (widget.store.profile.admin) ...[
                  Switch(
                    value: active,
                    onChanged: (value) => _configure(source, enabled: value),
                  ),
                  PopupMenuButton<String>(
                    tooltip: '${source.name}配置',
                    onSelected: (value) {
                      if (value == 'rename') {
                        unawaited(_rename(source));
                      } else if (value == 'remove') {
                        unawaited(_configure(source, removed: true));
                      }
                    },
                    itemBuilder: (_) => const [
                      PopupMenuItem(value: 'rename', child: Text('编辑名称')),
                      PopupMenuItem(value: 'remove', child: Text('移除站源')),
                    ],
                  ),
                  IconButton(
                    tooltip: '查看地址与人工验证',
                    onPressed: () => _sourceAccess(source),
                    icon: const Icon(Icons.verified_user_outlined),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 8),
            Text('最近更新：${sourceTimestamp(status?.updatedAt)}'),
            if (!active) const Text('已停用，浏览、搜索、播放与更新均暂停'),
            if (status != null && status.count > 0)
              Text(
                '已加载至第 ${status.page} 页${status.hasMore ? ' · 可继续加载' : ' · 当前分页已加载完'}',
              ),
            const SizedBox(height: 12),
            Wrap(
              spacing: 10,
              runSpacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                FilledButton.icon(
                  key: ValueKey('update-${source.id}'),
                  onPressed: enabled
                      ? () => _run(source, 'update', manual: true)
                      : null,
                  icon: const Icon(Icons.sync_rounded),
                  label: const Text('更新'),
                ),
                if (status != null && _resumableJob(status))
                  OutlinedButton.icon(
                    onPressed: enabled
                        ? () => _run(source, 'resume', manual: true)
                        : null,
                    icon: const Icon(Icons.play_arrow_rounded),
                    label: const Text('接着更新'),
                  ),
                OutlinedButton.icon(
                  key: ValueKey('check-${source.id}'),
                  onPressed: enabled ? () => _run(source, 'check') : null,
                  icon: const Icon(Icons.network_check),
                  label: const Text('检测连接与播放'),
                ),
                if (status?.running == true)
                  TextButton(
                    onPressed: pending ? null : () => _run(source, 'cancel'),
                    child: const Text('停止'),
                  ),
                PopupMenuButton<String>(
                  tooltip: '${source.name}更多操作',
                  enabled: enabled,
                  onSelected: (operation) => _run(source, operation),
                  itemBuilder: (_) => [
                    PopupMenuItem(
                      value: 'more',
                      enabled: status?.hasMore ?? true,
                      child: const Text('继续加载一页'),
                    ),
                    const PopupMenuItem(value: 'metadata', child: Text('补齐资料')),
                    if (source.id == 'huangdou')
                      PopupMenuItem(
                        value: 'vipMetadata',
                        enabled: (status?.unknownVip ?? 0) > 0,
                        child: Text('补齐 VIP 资料（${status?.unknownVip ?? 0} 部）'),
                      ),
                    const PopupMenuItem(
                      value: 'checkCatalog',
                      child: Text('仅检测目录'),
                    ),
                  ],
                ),
              ],
            ),
            if (busy) ...[
              const SizedBox(height: 12),
              LinearProgressIndicator(
                value: status != null && status.total > 0
                    ? (status.completed / status.total).clamp(0, 1)
                    : null,
              ),
              const SizedBox(height: 8),
              Text(sourceProgressText(status)),
            ] else if (status != null && status.stage.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(
                '${status.stage}${status.added > 0 ? ' · 新增 ${status.added} 部' : ''}',
              ),
            ],
            if (seconds > 0)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(
                  '请在 $seconds 秒后重试',
                  style: TextStyle(color: colors.error),
                ),
              ),
            if (error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: SelectableText(
                  error,
                  style: TextStyle(color: colors.error),
                ),
              ),
            if (status != null && status.storageError.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      status.storageError,
                      style: TextStyle(color: colors.error),
                    ),
                    TextButton.icon(
                      key: ValueKey('save-${source.id}'),
                      onPressed: manageable && !busy && active
                          ? () => _run(source, 'retrySave')
                          : null,
                      icon: const Icon(Icons.save_outlined),
                      label: const Text('重试保存'),
                    ),
                  ],
                ),
              ),
            if (health != null) ...[
              const Divider(height: 28),
              Row(
                children: [
                  Expanded(
                    child: Semantics(
                      expanded: healthExpanded,
                      child: Tooltip(
                        message: healthExpanded ? '收起检测详情' : '展开检测详情',
                        child: TextButton(
                          key: ValueKey('health-toggle-${source.id}'),
                          onPressed: () => setState(() {
                            if (healthExpanded) {
                              _expandedHealth.remove(source.id);
                            } else {
                              _expandedHealth.add(source.id);
                            }
                          }),
                          style: TextButton.styleFrom(
                            foregroundColor: colors.onSurface,
                            padding: const EdgeInsets.symmetric(
                              horizontal: 4,
                              vertical: 8,
                            ),
                          ),
                          child: Row(
                            children: [
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(health.label),
                                    Text(
                                      sourceTimestamp(health.checkedAt),
                                      style: Theme.of(
                                        context,
                                      ).textTheme.bodySmall,
                                    ),
                                  ],
                                ),
                              ),
                              const SizedBox(width: 8),
                              Icon(
                                healthExpanded
                                    ? Icons.expand_less_rounded
                                    : Icons.expand_more_rounded,
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                  IconButton(
                    tooltip: '复制诊断信息',
                    onPressed: () => _copy(source, status!),
                    icon: const Icon(Icons.copy_rounded),
                  ),
                ],
              ),
              if (healthExpanded) ...[
                if (health.sample.isNotEmpty) Text('检测剧集：${health.sample}'),
                for (final step in health.steps)
                  Padding(
                    padding: const EdgeInsets.only(top: 10),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Icon(
                          step.state == 'ok'
                              ? Icons.check_circle_outline
                              : Icons.error_outline,
                          size: 20,
                          color: step.state == 'ok'
                              ? colors.primary
                              : colors.error,
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text('${step.name}：${step.message}'),
                              if (step.host.isNotEmpty || step.httpStatus > 0)
                                Text(
                                  '${step.host}${step.httpStatus > 0 ? ' · HTTP ${step.httpStatus}' : ''} · ${step.elapsedMs} ms',
                                  style: Theme.of(context).textTheme.bodySmall,
                                ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ] else ...[
              const SizedBox(height: 12),
              const Text('尚未检测连接'),
            ],
          ],
        ),
      ),
    );
  }
}

class SourceDiagnosticsButton extends StatelessWidget {
  const SourceDiagnosticsButton({
    super.key,
    required this.repository,
    required this.store,
    required this.drama,
  });
  final AppRepository repository;
  final LocalStore store;
  final Drama drama;

  @override
  Widget build(BuildContext context) => TextButton.icon(
    onPressed: () => Navigator.push<void>(
      context,
      MaterialPageRoute(
        builder: (_) => SourcesScreen(
          repository: repository,
          store: store,
          initialSource: drama.source,
          drama: drama,
        ),
      ),
    ),
    icon: const Icon(Icons.network_check),
    label: const Text('站源诊断'),
  );
}
