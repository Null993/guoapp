import 'video_enhancement_preferences.dart';

const playbackSpeeds = [.5, .75, 1.0, 1.25, 1.5, 2.0, 3.0];

const videoFitTypes = ['contain', 'cover', 'fill', 'fitWidth', 'fitHeight'];

const fitTypeLabels = <String, String>{
  'contain': '自动',
  'cover': '裁剪',
  'fill': '拉伸',
  'fitWidth': '等宽',
  'fitHeight': '等高',
};

const fitTypeEntries = <({String key, String label})>[
  (key: 'contain', label: '自动'),
  (key: 'cover', label: '裁剪'),
  (key: 'fill', label: '拉伸'),
  (key: 'fitWidth', label: '等宽'),
  (key: 'fitHeight', label: '等高'),
];

const dragSeekSteps = [15, 30, 60, 90, 120];

class PlaybackPreferences {
  const PlaybackPreferences({
    this.speed = 1,
    this.quality = 0,
    this.autoAdvance = true,
    this.danmaku = true,
    this.preload = true,
    this.enhancement = const VideoEnhancementPreferences(),
    this.fitType = 'contain',
    this.slideVolumeBrightness = true,
    this.slideFullscreen = true,
    this.quickDoubleTap = false,
    this.doubleTapSeekSeconds = 10,
    this.dragSeekSeconds = 60,
    this.longPressSpeed = 3.0,
    this.autoLongPressSpeed = false,
    this.gestureSchema = 2,
  });

  final double speed;
  final int quality;
  final bool autoAdvance;
  final bool danmaku;
  final bool preload;
  final VideoEnhancementPreferences enhancement;

  final String fitType;

  final bool slideVolumeBrightness;

  final bool slideFullscreen;

  final bool quickDoubleTap;

  final int doubleTapSeekSeconds;

  final int dragSeekSeconds;

  final double longPressSpeed;

  final bool autoLongPressSpeed;

  final int gestureSchema;

  PlaybackPreferences copyWith({
    double? speed,
    int? quality,
    bool? autoAdvance,
    bool? danmaku,
    bool? preload,
    VideoEnhancementPreferences? enhancement,
    String? fitType,
    bool? slideVolumeBrightness,
    bool? slideFullscreen,
    bool? quickDoubleTap,
    int? doubleTapSeekSeconds,
    int? dragSeekSeconds,
    double? longPressSpeed,
    bool? autoLongPressSpeed,
    int? gestureSchema,
  }) => PlaybackPreferences(
    speed: speed ?? this.speed,
    quality: quality ?? this.quality,
    autoAdvance: autoAdvance ?? this.autoAdvance,
    danmaku: danmaku ?? this.danmaku,
    preload: preload ?? this.preload,
    enhancement: enhancement ?? this.enhancement,
    fitType: fitType ?? this.fitType,
    slideVolumeBrightness: slideVolumeBrightness ?? this.slideVolumeBrightness,
    slideFullscreen: slideFullscreen ?? this.slideFullscreen,
    quickDoubleTap: quickDoubleTap ?? this.quickDoubleTap,
    doubleTapSeekSeconds: doubleTapSeekSeconds ?? this.doubleTapSeekSeconds,
    dragSeekSeconds: dragSeekSeconds ?? this.dragSeekSeconds,
    longPressSpeed: longPressSpeed ?? this.longPressSpeed,
    autoLongPressSpeed: autoLongPressSpeed ?? this.autoLongPressSpeed,
    gestureSchema: gestureSchema ?? this.gestureSchema,
  );

  Map<String, dynamic> toJson() => {
    'speed': speed,
    'quality': quality,
    'autoAdvance': autoAdvance,
    'danmaku': danmaku,
    'preload': preload,
    'enhancement': enhancement.toJson(),
    'fitType': fitType,
    'slideVolumeBrightness': slideVolumeBrightness,
    'slideFullscreen': slideFullscreen,
    'quickDoubleTap': quickDoubleTap,
    'doubleTapSeekSeconds': doubleTapSeekSeconds,
    'dragSeekSeconds': dragSeekSeconds,
    'longPressSpeed': longPressSpeed,
    'autoLongPressSpeed': autoLongPressSpeed,
    'gestureSchema': gestureSchema,
  };

  factory PlaybackPreferences.fromJson(Map<String, dynamic> value) {
    final speed = (value['speed'] as num? ?? 1).toDouble();
    final quality = value['quality'] as int? ?? 0;
    final autoAdvance = value['autoAdvance'] as bool? ?? true;
    final danmaku = value['danmaku'] as bool? ?? true;
    final preload = value['preload'] as bool? ?? true;
    final fitType = value['fitType'] as String? ?? 'contain';
    final slideVolumeBrightness =
        value['slideVolumeBrightness'] as bool? ?? true;
    final slideFullscreen = value['slideFullscreen'] as bool? ?? true;
    final longPressSpeed = (value['longPressSpeed'] as num? ?? 3.0).toDouble();
    final autoLongPressSpeed = value['autoLongPressSpeed'] as bool? ?? false;
    final migrated = (value['gestureSchema'] as int? ?? 1) < 2;
    final quickDoubleTap = migrated
        ? false
        : (value['quickDoubleTap'] as bool? ?? false);
    final doubleTapSeekSeconds = (value['doubleTapSeekSeconds'] as int? ?? 10)
        .clamp(5, 60);
    final dragSeekSeconds = migrated
        ? 60
        : (value['dragSeekSeconds'] as int? ?? 60).clamp(15, 300);
    if (!playbackSpeeds.contains(speed) || quality < 0 || quality > 4320) {
      throw const FormatException('播放偏好无效');
    }
    if (!videoFitTypes.contains(fitType) ||
        !longPressSpeed.isFinite ||
        longPressSpeed < .5 ||
        longPressSpeed > 6) {
      throw const FormatException('播放偏好无效');
    }
    return PlaybackPreferences(
      speed: speed,
      quality: quality,
      autoAdvance: autoAdvance,
      danmaku: danmaku,
      preload: preload,
      enhancement: VideoEnhancementPreferences.fromJson(value['enhancement']),
      fitType: fitType,
      slideVolumeBrightness: slideVolumeBrightness,
      slideFullscreen: slideFullscreen,
      quickDoubleTap: quickDoubleTap,
      doubleTapSeekSeconds: doubleTapSeekSeconds,
      dragSeekSeconds: dragSeekSeconds,
      longPressSpeed: longPressSpeed,
      autoLongPressSpeed: autoLongPressSpeed,
      gestureSchema: 2,
    );
  }
}
