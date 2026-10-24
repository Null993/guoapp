import 'package:flutter/material.dart';

import 'local_store.dart';

Future<void> restoreBackupContent(
  BuildContext context,
  LocalStore store,
  String content,
) async {
  final epoch = store.profileEpoch;
  if (!store.profile.admin || store.locked) throw StateError('需要管理员权限');
  final data = store.validateBackup(content);
  final accepted = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: const Text('恢复备份？'),
      content: Text(
        '包含 ${(data['profiles'] as List).length} 个用户。将替换本机的用户、追剧、观看记录和偏好设置；已下载视频保留。恢复后使用备份内的管理员密码登录。',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(context, true),
          child: const Text('恢复'),
        ),
      ],
    ),
  );
  if (accepted != true || !context.mounted) return;
  if (store.profileEpoch != epoch || store.locked || !store.profile.admin)
    throw StateError("当前用户已变更");
  await store.importBackup(content);
  if (!context.mounted) return;
  Navigator.of(context).popUntil((route) => route.isFirst);
}
