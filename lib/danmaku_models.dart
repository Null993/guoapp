const danmakuWindowMs = 30000;
const danmakuLifetimeMs = 8000;
const danmakuMaxDurationMs = 86400000;

class DanmakuFilter {
  const DanmakuFilter({this.keywords = '', this.hideDuplicates = false});

  static const maxKeywordLength = 1000;
  final String keywords;
  final bool hideDuplicates;

  factory DanmakuFilter.fromJson(Object? value) {
    if (value is! Map) return const DanmakuFilter();
    final words = value['keywords'];
    return DanmakuFilter(
      keywords: words is String && words.length <= maxKeywordLength * 8
          ? words
          : '',
      hideDuplicates: value['hideDuplicates'] == true,
    );
  }

  Map<String, Object> toJson() => {
    'keywords': keywords,
    'hideDuplicates': hideDuplicates,
  };

  Iterable<DanmakuItem> apply(Iterable<DanmakuItem> items) sync* {
    final blocked = keywords
        .toLowerCase()
        .split('\n')
        .map((word) => word.trim())
        .where((word) => word.isNotEmpty)
        .toSet();
    if (blocked.isEmpty && !hideDuplicates) {
      yield* items;
      return;
    }
    final lastShown = <String, int>{};
    for (final item in items) {
      final message = item.text.toLowerCase().trim();
      if (blocked.any(message.contains)) continue;
      if (hideDuplicates) {
        final previous = lastShown[message];
        if (previous != null && item.timeMs - previous < danmakuLifetimeMs) {
          continue;
        }
        lastShown[message] = item.timeMs;
      }
      yield item;
    }
  }
}

class DanmakuItem {
  const DanmakuItem({
    required this.id,
    required this.text,
    required this.timeMs,
  });
  final String id;
  final String text;
  final int timeMs;
}

class DanmakuPage {
  const DanmakuPage({
    required this.episodeId,
    required this.startMs,
    required this.nextMs,
    required this.items,
  });
  final String episodeId;
  final int startMs;
  final int nextMs;
  final List<DanmakuItem> items;

  factory DanmakuPage.fromJson(
    Map<String, dynamic> json, {
    required String episodeId,
    required int startMs,
    required int durationMs,
  }) {
    final next = json['nextMs'];
    final rows = json['items'];
    if (json['episodeId'] != episodeId ||
        json['startMs'] != startMs ||
        next is! int ||
        next <= startMs ||
        next > durationMs ||
        rows is! List) {
      throw const FormatException('弹幕分集或时间范围不符');
    }
    final items = <DanmakuItem>[];
    final seen = <String>{};
    for (final row in rows.take(90)) {
      if (row is! Map) continue;
      final id = row['id'], text = row['text'], time = row['timeMs'];
      if (id is! String ||
          id.isEmpty ||
          id.length > 120 ||
          text is! String ||
          time is! int ||
          time < startMs ||
          time >= next ||
          seen.contains(id)) {
        continue;
      }
      final cleaned = text
          .replaceAll(RegExp(r'[\x00-\x1f\x7f-\x9f\u2028\u2029]'), ' ')
          .trim();
      if (cleaned.isEmpty) continue;
      seen.add(id);
      items.add(
        DanmakuItem(
          id: id,
          text: String.fromCharCodes(cleaned.runes.take(181)),
          timeMs: time,
        ),
      );
    }
    items.sort((a, b) => a.timeMs.compareTo(b.timeMs));
    return DanmakuPage(
      episodeId: episodeId,
      startMs: startMs,
      nextMs: next,
      items: List.unmodifiable(items),
    );
  }
}
