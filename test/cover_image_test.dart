import 'dart:io';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/catalog_updates.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

class CoverFixtureRepository extends FixtureRepository {
  final attempts = <bool>[];

  @override
  Future<String> cover(Drama drama, {bool force = false}) async {
    attempts.add(force);
    if (!force) {
      throw AppFailure('合成海报首次请求失败');
    }
    return File('test/fixtures/cover.png').absolute.path;
  }
}

class TransientCoverRepository extends FixtureRepository {
  int calls = 0;
  @override
  Future<String> cover(Drama drama, {bool force = false}) async {
    calls++;
    if (calls == 1) throw AppFailure('合成网络超时');
    return File('test/fixtures/cover.png').absolute.path;
  }
}

void main() {
  test(
    'metadata cache evicts old entries without retaining cover revisions',
    () {
      final cache = CatalogUpdates();
      final oldest = const Drama(
        id: 'hongguo:old',
        source: 'hongguo',
        title: '原始',
      );
      cache.publish(
        const Drama(id: 'hongguo:old', source: 'hongguo', title: '更新'),
        retryCover: true,
      );
      for (var i = 0; i < CatalogUpdates.maximumEntries; i++) {
        cache.publish(
          Drama(id: 'hongguo:$i', source: 'hongguo', title: '合成$i'),
        );
      }
      expect(cache.coverRevision(oldest.id), 0);
      expect(cache.current(oldest).title, '原始');
      cache.dispose();
    },
  );

  testWidgets('scrolling defers new cover work until idle', (tester) async {
    final repository = CoverFixtureRepository();
    final scroll = ScrollController();
    await tester.pumpWidget(
      MaterialApp(
        home: ListView(
          controller: scroll,
          children: [
            SizedBox(
              height: 150,
              child: CachedCoverImage(
                drama: FixtureRepository.free,
                repository: repository,
                placeholder: const SizedBox(),
              ),
            ),
          ],
        ),
      ),
    );
    scroll.position.isScrollingNotifier.value = true;
    await tester.pump(const Duration(milliseconds: 200));
    expect(repository.attempts, isEmpty);
    scroll.position.isScrollingNotifier.value = false;
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pumpAndSettle();
    expect(repository.attempts, [false]);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
    scroll.dispose();
  });

  testWidgets('a transient cover failure retries once after idle delay', (
    tester,
  ) async {
    final repository = TransientCoverRepository();
    await tester.pumpWidget(
      MaterialApp(
        home: SizedBox(
          width: 100,
          height: 150,
          child: CachedCoverImage(
            drama: FixtureRepository.free,
            repository: repository,
            placeholder: const SizedBox(),
          ),
        ),
      ),
    );
    await tester.runAsync(() async {
      await precacheImage(
        ResizeImage.resizeIfNeeded(
          440,
          null,
          FileImage(File('test/fixtures/cover.png').absolute),
        ),
        tester.element(find.byType(CachedCoverImage)),
      );
    });
    await tester.pumpAndSettle();
    expect(repository.calls, 1);
    expect(find.byTooltip('重试海报'), findsOneWidget);
    await tester.pump(const Duration(seconds: 15));
    await tester.pumpAndSettle();
    expect(repository.calls, 2);
    expect(find.byType(Image), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('failed cover can retry and render a decoded local file', (
    tester,
  ) async {
    final repository = CoverFixtureRepository();
    final widget = MaterialApp(
      home: SizedBox(
        width: 100,
        height: 150,
        child: CachedCoverImage(
          drama: FixtureRepository.free,
          repository: repository,
          placeholder: const SizedBox(),
        ),
      ),
    );
    await tester.pumpWidget(widget);
    await tester.pumpAndSettle();
    expect(find.byTooltip('重试海报'), findsOneWidget);
    Object? decodeFailure;
    await tester.runAsync(() async {
      await precacheImage(
        ResizeImage.resizeIfNeeded(
          440,
          null,
          FileImage(File('test/fixtures/cover.png').absolute),
        ),
        tester.element(find.byType(CachedCoverImage)),
        onError: (error, stack) => decodeFailure = error,
      );
    });
    expect(decodeFailure, isNull);
    await tester.tap(find.byTooltip('重试海报'));
    await tester.pumpAndSettle();
    expect(repository.attempts, [false, true]);
    expect(find.byType(Image), findsOneWidget);
    final image = tester.widget<Image>(find.byType(Image));
    final provider = image.image as ResizeImage;
    expect(provider.imageProvider, isA<FileImage>());
    expect(tester.widget<RawImage>(find.byType(RawImage)).image, isNotNull);
    await tester.pumpWidget(widget);
    await tester.pumpAndSettle();
    expect(repository.attempts, [false, true]);
    expect(tester.takeException(), isNull);
  });
}
