import 'dart:convert';
import 'dart:io';

import 'package:integration_test/integration_test_driver_extended.dart';

Future<void> main() async {
  final output = Directory(
    Platform.environment['SOURCE_DRIVER_OUTPUT'] ?? 'build/source-playback',
  );
  await output.create(recursive: true);
  await integrationDriver(
    writeResponseOnFailure: true,
    responseDataCallback: (data) async {
      final report = Map<String, dynamic>.from(data ?? {})
        ..remove('screenshots');
      final platform = report['platform'] ?? 'unknown';
      final mode = report['proxyMode'] ?? 'auto';
      final encoded = const JsonEncoder.withIndent('  ').convert(report);
      await File(
        '${output.path}/$platform-driver.json',
      ).writeAsString(encoded, flush: true);
      await File(
        '${output.path}/$platform-$mode-driver.json',
      ).writeAsString(encoded, flush: true);
      final required = (report['requiredPerSource'] as num?)?.toInt() ?? 0;
      final sources = report['sources'] as List? ?? const [];
      if (required < 1 ||
          sources.isEmpty ||
          sources.any(
            (source) =>
                source['status'] != 'passed' ||
                (source['successCount'] as num? ?? 0) < required,
          )) {
        throw StateError('实播报告缺失、初始化失败或存在未达标站源；请查看原始报告。');
      }
    },
  );
}
