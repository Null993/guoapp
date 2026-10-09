import 'dart:convert';

import 'package:duanju_app/local_snapshot.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/resource_settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_platform_interface.dart';

import 'fixtures.dart';

class _SettingsRepository extends FixtureRepository {
  _SettingsRepository(this.settings);
  ResourceSettings settings;

  @override
  Future<ResourceSettings> resourceSettings() async => settings;

  @override
  Future<ResourceSettings> saveResourceSettings(ResourceSettings value) async =>
      settings = value;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<LocalStore> open([Map<String, Object> snapshot = const {}]) async {
    SharedPreferences.resetStatic();
    SharedPreferencesStorePlatform
        .instance = InMemorySharedPreferencesStore.withData({
      for (final entry in snapshot.entries) 'flutter.${entry.key}': entry.value,
    });
    final store = LocalStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  test('allowInsecureTLS is a registered snapshot field', () {
    expect(LocalSnapshot.owns('allowInsecureTLS'), isTrue);
  });

  test('allowInsecureTLS defaults to off and survives restart', () async {
    final store = await open();
    expect(store.allowInsecureTLS, isFalse);
    await store.setAllowInsecureTLS(true);
    expect(store.allowInsecureTLS, isTrue);
    final saved = store.preferences.getString(LocalSnapshot.storageKey);
    expect(saved, isNotNull);
    final restarted = await open({LocalSnapshot.storageKey: saved!});
    expect(restarted.allowInsecureTLS, isTrue);
    expect(restarted.configurationError, isNull);
  });

  test('resource settings carry allowInsecureTLS to the native core', () {
    const settings = ResourceSettings(
      proxyMode: 'direct',
      allowInsecureTLS: true,
    );
    expect(settings.toJson()['allowInsecureTLS'], isTrue);
    expect(
      ResourceSettings.fromJson(settings.toJson()).allowInsecureTLS,
      isTrue,
    );
    expect(const ResourceSettings().allowInsecureTLS, isFalse);
    expect(ResourceSettings.fromJson(const {}).allowInsecureTLS, isFalse);
  });

  test(
    'backup restore keeps the local TLS switch aligned with native',
    () async {
      final store = await open();
      expect(store.allowInsecureTLS, isFalse);
      await store.setAllowInsecureTLS(true);
      final backup = await store.exportBackup();
      expect(
        (jsonDecode(backup) as Map<String, dynamic>).containsKey(
          'allowInsecureTLS',
        ),
        isFalse,
      );
      await store.importBackup(backup);
      expect(store.allowInsecureTLS, isTrue);
      final fresh = await open();
      expect(fresh.allowInsecureTLS, isFalse);
      await fresh.importBackup(backup);
      expect(fresh.allowInsecureTLS, isFalse);
    },
  );

  testWidgets('resource screen reports the effective native TLS value', (
    tester,
  ) async {
    final store = await open();
    expect(store.allowInsecureTLS, isFalse);
    final repository = _SettingsRepository(
      const ResourceSettings(allowInsecureTLS: true),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: ResourceSettingsScreen(repository: repository, store: store),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<SwitchListTile>(
            find.widgetWithText(SwitchListTile, '允许本机代理替换证书（关闭 TLS 校验）'),
          )
          .value,
      isTrue,
    );
    expect(store.allowInsecureTLS, isTrue);
  });
}
