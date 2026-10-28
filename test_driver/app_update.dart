import 'dart:convert';
import 'dart:io';

import 'package:integration_test/integration_test_driver_extended.dart';

Future<void> main() async {
  await integrationDriver(
    writeResponseOnFailure: true,
    responseDataCallback: (data) async {
      final file = File(
        Platform.environment['UPDATE_DRIVER_REPORT'] ??
            'build/android-update-install.json',
      );
      await file.parent.create(recursive: true);
      await file.writeAsString(
        const JsonEncoder.withIndent('  ').convert(data),
      );
      if (data?['ok'] != true) throw StateError('Android 更新安装器冒烟未通过');
    },
  );
}
