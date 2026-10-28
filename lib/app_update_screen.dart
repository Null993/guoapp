import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:window_manager/window_manager.dart';

import 'app_layout.dart';
import 'core_bridge.dart';
import 'local_store.dart';

class AppUpdateScreen extends StatefulWidget {
  const AppUpdateScreen({
    super.key,
    required this.repository,
    required this.store,
  });
  final AppRepository repository;
  final LocalStore store;
  @override
  State<AppUpdateScreen> createState() => _AppUpdateScreenState();
}

class _AppUpdateScreenState extends State<AppUpdateScreen> {
  Map<String, dynamic> _state = {};
  List<String> _abis = [];
  Timer? _timer;
  bool _busy = false;
  bool _polling = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_initialize());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _initialize() async {
    await _run(() async {
      if (Platform.isAndroid) {
        final info = await AppDevice.channel.invokeMapMethod<String, dynamic>(
          'appUpdateInfo',
        );
        _abis = (info?['abis'] as List? ?? []).cast<String>();
      }
      _state = await widget.repository.appUpdate('status');
      _watch();
    });
  }

  Future<void> _run(Future<void> Function() action) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await action();
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _watch() {
    _timer?.cancel();
    if (_state['status'] != 'downloading') return;
    _timer = Timer.periodic(const Duration(milliseconds: 600), (_) async {
      if (_polling || !mounted) return;
      _polling = true;
      try {
        final state = await widget.repository.appUpdate('status');
        if (!mounted) return;
        setState(() => _state = state);
        if (state['status'] != 'downloading') _timer?.cancel();
      } catch (error) {
        _timer?.cancel();
        if (mounted) setState(() => _error = error.toString());
      } finally {
        _polling = false;
      }
    });
  }

  Future<void> _check() => _run(() async {
    _state = await widget.repository.appUpdate('check', {
      'platform': Platform.operatingSystem,
      'abis': _abis,
    });
  });

  Future<void> _download() => _run(() async {
    _state = await widget.repository.appUpdate('download');
    _watch();
  });

  Future<void> _install({bool rollback = false}) async {
    final accepted = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(rollback ? '回退到上一版本？' : '安装更新？'),
        content: Text(
          Platform.isWindows
              ? '将暂停下载并退出应用，替换整套程序后重新启动。更新失败会自动恢复原版本，用户资料保留。'
              : '将打开 Android 系统安装器，按系统提示完成覆盖安装，用户资料保留。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('继续'),
          ),
        ],
      ),
    );
    if (accepted != true || !mounted) return;
    await _run(() async {
      if (Platform.isWindows && widget.store.canDownload) {
        await widget.repository.controlDownloads('pauseAll');
      }
      _state = await widget.repository.appUpdate(
        rollback ? 'rollback' : 'install',
      );
      if (Platform.isWindows) {
        await windowManager.close();
      } else if (Platform.isAndroid) {
        final status = await AppDevice.channel.invokeMethod<String>(
          'installAppUpdate',
          {'path': _state['path']},
        );
        if (status == 'permission_required') {
          if (mounted) setState(() => _error = '请在系统页面允许安装应用，返回后再次点击安装。');
        }
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final asset = _state['asset'] as Map?;
    final size = (asset?['size'] as num?)?.toInt() ?? 0;
    final received = (_state['received'] as num?)?.toInt() ?? 0;
    final status = _state['status'];
    final downloading = status == 'downloading';
    final admin = widget.store.profile.admin && !widget.store.locked;
    return Scaffold(
      appBar: AppBar(title: const Text('检查更新')),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: ListView(
            padding: const EdgeInsets.all(20),
            children: [
              Text(
                '$appName $appVersion+$appBuildCode',
                style: Theme.of(context).textTheme.titleLarge,
              ),
              const SizedBox(height: 8),
              const SelectableText('更新来源：GitHub · Null993/guoapp · 稳定 Release'),
              if (_state['version'] != null)
                Padding(
                  padding: const EdgeInsets.only(top: 16),
                  child: Text('已发布版本：${_state['version']}'),
                ),
              if (_state['message'] case final String message)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: Text(message),
                ),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  child: SelectableText(
                    _error!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ),
              if (_busy || downloading)
                LinearProgressIndicator(
                  value: downloading && size > 0
                      ? (received / size).clamp(0, 1)
                      : null,
                ),
              if (downloading)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(
                    '${(received / 1048576).toStringAsFixed(1)} / ${(size / 1048576).toStringAsFixed(1)} MB',
                  ),
                ),
              if (asset != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(
                    '${asset['name']} · ${(size / 1048576).toStringAsFixed(1)} MB',
                  ),
                ),
              const SizedBox(height: 12),
              Wrap(
                spacing: 12,
                runSpacing: 12,
                children: [
                  OutlinedButton(
                    onPressed: _busy || downloading ? null : _check,
                    child: const Text('检查更新'),
                  ),
                  if (admin &&
                      {'available', 'failed', 'cancelled'}.contains(status) &&
                      asset != null)
                    FilledButton(
                      onPressed: _busy ? null : _download,
                      child: Text(status == 'available' ? '下载新版本' : '重新下载'),
                    ),
                  if (admin && downloading)
                    OutlinedButton(
                      onPressed: _busy
                          ? null
                          : () => _run(() async {
                              _state = await widget.repository.appUpdate(
                                'cancel',
                              );
                            }),
                      child: const Text('取消下载'),
                    ),
                  if (admin && status == 'ready')
                    FilledButton(
                      onPressed: _busy ? null : () => _install(),
                      child: const Text('安装更新'),
                    ),
                  if (admin &&
                      Platform.isWindows &&
                      _state['rollbackAvailable'] == true &&
                      !downloading)
                    OutlinedButton(
                      onPressed: _busy ? null : () => _install(rollback: true),
                      child: const Text('回退上一版本'),
                    ),
                ],
              ),
              if (!admin)
                const Padding(
                  padding: EdgeInsets.only(top: 12),
                  child: Text('请切换到管理员用户后下载或安装更新。'),
                ),
              if (_state['notes'] case final String notes
                  when notes.isNotEmpty) ...[
                const Divider(height: 36),
                Text('更新说明', style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 12),
                SelectableText(notes),
              ],
              if (_state['url'] case final String url)
                TextButton(
                  onPressed: () async {
                    await Clipboard.setData(ClipboardData(text: url));
                    if (context.mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('Release 地址已复制')),
                      );
                    }
                  },
                  child: const Text('复制 GitHub Release 地址'),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
