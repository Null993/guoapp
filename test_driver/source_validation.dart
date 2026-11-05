import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:integration_test/integration_test_driver_extended.dart';

Future<void> main() async {
  await integrationDriver(
    writeResponseOnFailure: true,
    responseDataCallback: (data) async {
      final directory = Directory(
        Platform.environment['SOURCE_VALIDATION_OUTPUT'] ??
            'build/source-validation',
      );
      await directory.create(recursive: true);
      final report = Map<String, dynamic>.from(data ?? {})
        ..remove('screenshots');
      final exported = report['export'];
      if (report['platform'] == 'android' && exported is Map) {
        final path = exported['path'] as String? ?? '';
        if (path.startsWith(
              '/data/user/0/com.duanju.duanju_app.debug/files/source-jobs-',
            ) &&
            path.endsWith('/resumed-results.json') &&
            !path.split('/').contains('..')) {
          final result = await Process.run('adb', [
            '-s',
            'emulator-5554',
            'exec-out',
            'run-as',
            'com.duanju.duanju_app.debug',
            'cat',
            path,
          ], stdoutEncoding: null);
          if (result.exitCode == 0) {
            final bytes = result.stdout as List<int>;
            final file = File('${directory.path}/resumed-results.json');
            await file.writeAsBytes(bytes, flush: true);
            report['continuationExport'] = {
              'path': file.path,
              'bytes': bytes.length,
              'sha256': sha256.convert(bytes).toString(),
            };
          }
        }
      }
      await File(
        '${directory.path}/${report['platform'] ?? 'unknown'}.json',
      ).writeAsString(
        const JsonEncoder.withIndent('  ').convert(report),
        flush: true,
      );
      if (report['ok'] != true) throw StateError('站源验证未通过，详见报告');
    },
  );
}
