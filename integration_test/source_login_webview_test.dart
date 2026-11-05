import 'dart:io';

import 'package:duanju_app/source_login_smoke.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('native login browser extracts storage and HttpOnly cookies', (
    tester,
  ) async {
    Map<String, bool>? report;
    Object? failure;
    final work = smokeSourceLoginWebView()
        .then((value) => report = value)
        .catchError((Object error) {
          failure = error;
          return <String, bool>{};
        });
    final elapsed = Stopwatch()..start();
    while (report == null &&
        failure == null &&
        elapsed.elapsed < const Duration(seconds: 45)) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    await work;
    binding.reportData = {
      'platform': Platform.operatingSystem,
      'kind': 'synthetic native login browser',
      'ok': report?.values.every((value) => value) == true,
      'checks': report,
      if (failure != null) 'error': failure.toString(),
    };
    expect(failure, isNull);
    expect(report, {'loginWebView': true, 'loginHttpOnlyCookie': true});
  });
}
