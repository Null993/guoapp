import 'dart:io';

import 'package:duanju_app/app_layout.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:path_provider/path_provider.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final report = <String, dynamic>{
    'version': '0.2.82+89',
    'platform': 'android',
    'ok': false,
  };
  testWidgets('Android validates an APK and opens the system installer', (
    tester,
  ) async {
    expect(Platform.isAndroid, isTrue);
    final directory = await getApplicationSupportDirectory();
    final updates = Directory('${directory.path}/updates');
    await updates.create(recursive: true);
    final invalid = File('${updates.path}/invalid.apk');
    await invalid.writeAsString('synthetic invalid APK');
    await expectLater(
      AppDevice.channel.invokeMethod<String>('installAppUpdate', {
        'path': invalid.path,
      }),
      throwsA(isA<PlatformException>()),
    );
    report['invalidApkRejected'] = true;
    await expectLater(
      AppDevice.channel.invokeMethod<String>('installAppUpdate', {
        'path': '/invalid/outside.apk',
      }),
      throwsA(isA<PlatformException>()),
    );
    report['outsidePathRejected'] = true;
    final file = File('${updates.path}/installer-fixture.apk');
    debugPrint('APP_UPDATE_INSTALLER_READY ${file.path}');
    await tester.runAsync(() async {
      final deadline = DateTime.now().add(const Duration(seconds: 90));
      while (!await file.exists()) {
        if (DateTime.now().isAfter(deadline)) {
          throw StateError('Installer fixture was not supplied');
        }
        await Future<void>.delayed(const Duration(milliseconds: 500));
      }
      final status = await AppDevice.channel.invokeMethod<String>(
        'installAppUpdate',
        {'path': file.path},
      );
      expect(status, 'installer_opened');
      report['validPackageNameSignatureAndHigherVersion'] = true;
      report['installerOpened'] = true;
      debugPrint('APP_UPDATE_INSTALLER_OPENED');
      await Future<void>.delayed(const Duration(seconds: 5));
    });
    report['ok'] = true;
    binding.reportData = report;
  });
}
