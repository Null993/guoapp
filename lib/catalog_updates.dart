import 'package:flutter/foundation.dart';

import 'models.dart';

class CatalogUpdates extends ChangeNotifier {
  static const maximumEntries = 2048;

  final _items = <String, Drama>{};
  final _coverRetries = <String, int>{};
  Drama? latest;

  Drama current(Drama drama) =>
      _items[drama.id] == null ? drama : drama.merge(_items[drama.id]!);

  int coverRevision(String id) => _coverRetries[id] ?? 0;

  void publish(Drama drama, {bool retryCover = false}) {
    final previous = _items[drama.id];
    latest = previous?.merge(drama) ?? drama;
    _items.remove(drama.id);
    _items[drama.id] = latest!;
    while (_items.length > maximumEntries) {
      final oldest = _items.keys.first;
      _items.remove(oldest);
      _coverRetries.remove(oldest);
    }
    if (retryCover) _coverRetries[drama.id] = coverRevision(drama.id) + 1;
    notifyListeners();
  }
}
