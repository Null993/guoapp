import 'package:duanju_app/app_update_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class UpdateFixture extends FixtureRepository {
  final commands = <String>[];
  bool failUpdate = false;
  @override
  Future<Map<String, dynamic>> appUpdate(
    String command, [
    Map<String, dynamic> input = const {},
  ]) async {
    commands.add(command);
    if (command == 'status') return {'status': 'current'};
    if (failUpdate) throw StateError('GitHub HTTP 403');
    if (command == 'download') {
      return {'status': 'ready', 'message': '下载完成，SHA256 校验通过'};
    }
    return {
      'status': 'available',
      'version': '0.2.83+90',
      'notes': '修复说明',
      'message': '发现新版本，可下载并安装',
      'asset': {'name': 'zhenguojian-0.2.83+90-windows-x64.zip', 'size': 1024},
    };
  }
}

void main() {
  testWidgets(
    'check, download and cancel installation without exiting the app',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final repository = UpdateFixture();
      await tester.pumpWidget(
        MaterialApp(
          home: AppUpdateScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(OutlinedButton, '检查更新'));
      await tester.pumpAndSettle();
      expect(find.text('已发布版本：0.2.83+90'), findsOneWidget);
      expect(find.text('修复说明'), findsOneWidget);
      await tester.tap(find.text('下载新版本'));
      await tester.pumpAndSettle();
      expect(find.text('下载完成，SHA256 校验通过'), findsOneWidget);
      await tester.tap(find.text('安装更新'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(repository.commands, ['status', 'check', 'download']);
    },
  );

  testWidgets('GitHub errors do not claim the installed version is current', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final repository = UpdateFixture()..failUpdate = true;
    await tester.pumpWidget(
      MaterialApp(
        home: AppUpdateScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(OutlinedButton, '检查更新'));
    await tester.pumpAndSettle();
    expect(find.textContaining('GitHub HTTP 403'), findsOneWidget);
    expect(find.text('当前已是最新稳定版本'), findsNothing);
  });
}
