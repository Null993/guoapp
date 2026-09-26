package com.duanju.duanju_app

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.TextView
import android.widget.Toast
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import org.json.JSONArray
import org.json.JSONObject
import java.util.Locale
import kotlin.math.abs

data class NativeEpisode(
    val index: Int,
    val number: Int,
    val title: String,
    val url: String,
    val headers: Map<String, String> = emptyMap()
)

class ExoPlayerActivity : Activity() {

    private var player: ExoPlayer? = null
    private lateinit var playerView: PlayerView
    private lateinit var rootLayout: FrameLayout
    private lateinit var controlsLayout: FrameLayout
    private lateinit var topBar: LinearLayout
    private lateinit var bottomBar: LinearLayout
    private lateinit var titleText: TextView
    private lateinit var playPauseBtn: TextView
    private lateinit var timeCurrentText: TextView
    private lateinit var timeDurationText: TextView
    private lateinit var seekBar: SeekBar
    private lateinit var loadingSpinner: ProgressBar
    private lateinit var brightnessHud: LinearLayout
    private lateinit var brightnessBar: ProgressBar
    private lateinit var brightnessPercent: TextView
    private lateinit var episodesDrawer: LinearLayout
    private lateinit var episodesContainer: LinearLayout

    private var dramaTitle: String = "短剧"
    private var episodesList: MutableList<NativeEpisode> = mutableListOf()
    private var currentIndex: Int = 0
    private var initialPositionMs: Long = 0L

    private var isControlsVisible: Boolean = true
    private var isEpisodesDrawerVisible: Boolean = false
    private var isSeeking: Boolean = false

    private val handler = Handler(Looper.getMainLooper())
    private val hideControlsRunnable = Runnable { hideControls() }
    private val hideBrightnessRunnable = Runnable { brightnessHud.visibility = View.GONE }
    private val updateProgressRunnable = object : Runnable {
        override fun run() {
            updateProgress()
            handler.postDelayed(this, 500)
        }
    }

    // 屏幕左侧亮度滑动手势变量
    private var touchStartY = 0f
    private var touchStartX = 0f
    private var isBrightnessAdjusting = false
    private var initialBrightness = 0.5f

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        window.decorView.systemUiVisibility = (
            View.SYSTEM_UI_FLAG_FULLSCREEN
                or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                or View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                or View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                or View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                or View.SYSTEM_UI_FLAG_LAYOUT_STABLE
        )

        parseIntentData()
        buildUi()
        initExoPlayer()
        playCurrentEpisode(initialPositionMs)
    }

    private fun parseIntentData() {
        dramaTitle = intent.getStringExtra("title") ?: "短剧"
        currentIndex = intent.getIntExtra("index", 0)
        initialPositionMs = intent.getLongExtra("position", 0L)

        val rawEpisodesJson = intent.getStringExtra("episodes")
        if (!rawEpisodesJson.isNullOrEmpty()) {
            try {
                val array = JSONArray(rawEpisodesJson)
                for (i in 0 until array.length()) {
                    val obj = array.getJSONObject(i)
                    val idx = obj.optInt("index", i)
                    val num = obj.optInt("number", idx + 1)
                    val t = obj.optString("title", "第 $num 集")
                    val u = obj.optString("url", "")
                    val headersMap = mutableMapOf<String, String>()
                    val headersObj = obj.optJSONObject("headers")
                    if (headersObj != null) {
                        val keys = headersObj.keys()
                        while (keys.hasNext()) {
                            val k = keys.next()
                            headersMap[k] = headersObj.optString(k, "")
                        }
                    }
                    episodesList.add(NativeEpisode(idx, num, t, u, headersMap))
                }
            } catch (_: Exception) {}
        }

        // 如果没有传入列表，使用单一当前 URL
        if (episodesList.isEmpty()) {
            val singleUrl = intent.getStringExtra("url") ?: ""
            episodesList.add(
                NativeEpisode(
                    index = 0,
                    number = 1,
                    title = intent.getStringExtra("episodeTitle") ?: "第 1 集",
                    url = singleUrl
                )
            )
            currentIndex = 0
        }
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    private fun createCardBackground(color: Int, radiusDp: Int): GradientDrawable {
        return GradientDrawable().apply {
            setColor(color)
            cornerRadius = dp(radiusDp).toFloat()
        }
    }

    @SuppressLint("ClickableViewAccessibility")
    private fun buildUi() {
        rootLayout = FrameLayout(this).apply {
            setBackgroundColor(Color.BLACK)
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        }

        // 1. ExoPlayer 核心视图 (使用原生硬件直出 SurfaceView，老电视满帧不掉帧)
        playerView = PlayerView(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
            useController = false
            setShutterBackgroundColor(Color.BLACK)
        }
        rootLayout.addView(playerView)

        // 2. 播控整体浮层
        controlsLayout = FrameLayout(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        }

        // 顶部操作栏
        topBar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(18), dp(14), dp(18), dp(14))
            background = createCardBackground(Color.parseColor("#99000000"), 0)
            layoutParams = FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
            ).apply { gravity = Gravity.TOP }
        }

        val backBtn = TextView(this).apply {
            text = "◀ 返回"
            setTextColor(Color.WHITE)
            textSize = 15f
            setPadding(dp(10), dp(6), dp(14), dp(6))
            isFocusable = true
            setOnClickListener { finishWithResult() }
        }
        topBar.addView(backBtn)

        titleText = TextView(this).apply {
            text = "$dramaTitle · 第 ${episodesList.getOrNull(currentIndex)?.number ?: 1} 集"
            setTextColor(Color.WHITE)
            textSize = 17f
            typeface = Typeface.DEFAULT_BOLD
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
        }
        topBar.addView(titleText)

        val exoBadge = TextView(this).apply {
            text = "ExoPlayer 内核硬解"
            setTextColor(Color.parseColor("#4CAF50"))
            textSize = 13f
            typeface = Typeface.DEFAULT_BOLD
            setPadding(dp(10), dp(4), dp(10), dp(4))
            background = createCardBackground(Color.parseColor("#264CAF50"), 12)
        }
        topBar.addView(exoBadge)

        controlsLayout.addView(topBar)

        // 中间大播放/暂停图标
        playPauseBtn = TextView(this).apply {
            text = "❚❚"
            setTextColor(Color.WHITE)
            textSize = 28f
            gravity = Gravity.CENTER
            background = createCardBackground(Color.parseColor("#B3000000"), 35)
            layoutParams = FrameLayout.LayoutParams(dp(70), dp(70)).apply {
                gravity = Gravity.CENTER
            }
            isFocusable = true
            setOnClickListener { togglePlayPause() }
        }
        controlsLayout.addView(playPauseBtn)

        // 底部操作栏
        bottomBar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(18), dp(12), dp(18), dp(14))
            background = createCardBackground(Color.parseColor("#99000000"), 0)
            layoutParams = FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
            ).apply { gravity = Gravity.BOTTOM }
        }

        timeCurrentText = TextView(this).apply {
            text = "00:00"
            setTextColor(Color.parseColor("#E0E0E0"))
            textSize = 13f
        }
        bottomBar.addView(timeCurrentText)

        seekBar = SeekBar(this).apply {
            max = 1000
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                leftMargin = dp(12)
                rightMargin = dp(12)
            }
            setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
                override fun onProgressChanged(sb: SeekBar?, progress: Int, fromUser: Boolean) {
                    if (fromUser && player != null) {
                        val duration = player!!.duration.coerceAtLeast(1L)
                        val targetMs = (duration * (progress / 1000f)).toLong()
                        timeCurrentText.text = formatTime(targetMs)
                    }
                }
                override fun onStartTrackingTouch(sb: SeekBar?) {
                    isSeeking = true
                    handler.removeCallbacks(hideControlsRunnable)
                }
                override fun onStopTrackingTouch(sb: SeekBar?) {
                    if (player != null) {
                        val duration = player!!.duration.coerceAtLeast(1L)
                        val targetMs = (duration * (seekBar.progress / 1000f)).toLong()
                        player!!.seekTo(targetMs)
                    }
                    isSeeking = false
                    scheduleHideControls()
                }
            })
        }
        bottomBar.addView(seekBar)

        timeDurationText = TextView(this).apply {
            text = "00:00"
            setTextColor(Color.parseColor("#9E9E9E"))
            textSize = 13f
        }
        bottomBar.addView(timeDurationText)

        val episodesBtn = TextView(this).apply {
            text = "选集"
            setTextColor(Color.WHITE)
            textSize = 14f
            typeface = Typeface.DEFAULT_BOLD
            setPadding(dp(14), dp(6), dp(14), dp(6))
            background = createCardBackground(Color.parseColor("#33FFFFFF"), 14)
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
            ).apply { leftMargin = dp(14) }
            isFocusable = true
            setOnClickListener { toggleEpisodesDrawer() }
        }
        bottomBar.addView(episodesBtn)

        val nextBtn = TextView(this).apply {
            text = "下一集 ▶"
            setTextColor(Color.WHITE)
            textSize = 14f
            typeface = Typeface.DEFAULT_BOLD
            setPadding(dp(14), dp(6), dp(14), dp(6))
            background = createCardBackground(Color.parseColor("#FF5722"), 14)
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
            ).apply { leftMargin = dp(10) }
            isFocusable = true
            setOnClickListener { playNextEpisode() }
        }
        bottomBar.addView(nextBtn)

        controlsLayout.addView(bottomBar)
        rootLayout.addView(controlsLayout)

        // 3. 经典居中毛玻璃深色卡片亮度调节 HUD (西瓜视频风格，完全移除滑动音量)
        brightnessHud = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(dp(20), dp(16), dp(20), dp(16))
            background = createCardBackground(Color.parseColor("#D9212121"), 16)
            layoutParams = FrameLayout.LayoutParams(dp(150), dp(115)).apply {
                gravity = Gravity.CENTER
            }
            visibility = View.GONE
        }

        val sunIcon = TextView(this).apply {
            text = "☀"
            textSize = 28f
            setTextColor(Color.WHITE)
            gravity = Gravity.CENTER
        }
        brightnessHud.addView(sunIcon)

        brightnessBar = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            max = 100
            layoutParams = LinearLayout.LayoutParams(dp(100), dp(6)).apply {
                topMargin = dp(10)
                bottomMargin = dp(8)
            }
        }
        brightnessHud.addView(brightnessBar)

        brightnessPercent = TextView(this).apply {
            text = "50%"
            setTextColor(Color.WHITE)
            textSize = 14f
            typeface = Typeface.DEFAULT_BOLD
            gravity = Gravity.CENTER
        }
        brightnessHud.addView(brightnessPercent)

        rootLayout.addView(brightnessHud)

        // 4. 侧边抽屉选集浮层
        episodesDrawer = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            background = createCardBackground(Color.parseColor("#F2181818"), 0)
            setPadding(dp(16), dp(20), dp(16), dp(20))
            layoutParams = FrameLayout.LayoutParams(dp(280), ViewGroup.LayoutParams.MATCH_PARENT).apply {
                gravity = Gravity.END
            }
            visibility = View.GONE
        }

        val drawerHeader = TextView(this).apply {
            text = "剧集列表 (共 ${episodesList.size} 集)"
            setTextColor(Color.WHITE)
            textSize = 16f
            typeface = Typeface.DEFAULT_BOLD
            setPadding(0, 0, 0, dp(14))
        }
        episodesDrawer.addView(drawerHeader)

        episodesContainer = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
        }

        val scroll = ScrollView(this).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                0,
                1f
            )
            addView(episodesContainer)
        }
        episodesDrawer.addView(scroll)

        populateEpisodesDrawer()
        rootLayout.addView(episodesDrawer)

        // 5. 缓冲菊花加载条
        loadingSpinner = ProgressBar(this).apply {
            layoutParams = FrameLayout.LayoutParams(dp(54), dp(54)).apply {
                gravity = Gravity.CENTER
            }
            visibility = View.VISIBLE
        }
        rootLayout.addView(loadingSpinner)

        // 6. 核心触摸手势监听：左侧垂直滑动调亮度，单击显隐播控/暂停
        rootLayout.setOnTouchListener { _, event ->
            handleTouchGesture(event)
        }

        setContentView(rootLayout)
        scheduleHideControls()
        handler.post(updateProgressRunnable)
    }

    private fun populateEpisodesDrawer() {
        episodesContainer.removeAllViews()
        for (ep in episodesList) {
            val isCurrent = ep.index == currentIndex
            val epItem = TextView(this).apply {
                text = "第 ${ep.number} 集  ${if (isCurrent) "▶ 正在播放" else ""}"
                textSize = 15f
                setTextColor(if (isCurrent) Color.parseColor("#FF5722") else Color.WHITE)
                typeface = if (isCurrent) Typeface.DEFAULT_BOLD else Typeface.DEFAULT
                setPadding(dp(14), dp(12), dp(14), dp(12))
                background = createCardBackground(
                    if (isCurrent) Color.parseColor("#33FF5722") else Color.parseColor("#1FFFFFFF"),
                    8
                )
                layoutParams = LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.WRAP_CONTENT
                ).apply { bottomMargin = dp(8) }
                isFocusable = true
                setOnClickListener {
                    toggleEpisodesDrawer()
                    playEpisode(ep.index)
                }
            }
            episodesContainer.addView(epItem)
        }
    }

    private fun handleTouchGesture(event: MotionEvent): Boolean {
        val screenWidth = resources.displayMetrics.widthPixels
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                touchStartX = event.rawX
                touchStartY = event.rawY
                isBrightnessAdjusting = false
                val lp = window.attributes
                initialBrightness = if (lp.screenBrightness >= 0f) lp.screenBrightness else 0.5f
            }
            MotionEvent.ACTION_MOVE -> {
                val dx = event.rawX - touchStartX
                val dy = event.rawY - touchStartY
                // 仅当在屏幕左侧 40% 区域，并且垂直位移大于 25dp 时判定为调节亮度
                if (touchStartX < screenWidth * 0.45f && abs(dy) > dp(25) && abs(dy) > abs(dx)) {
                    isBrightnessAdjusting = true
                    val delta = -dy / (resources.displayMetrics.heightPixels * 0.7f)
                    val newBrightness = (initialBrightness + delta).coerceIn(0.01f, 1.0f)
                    applyBrightness(newBrightness)
                }
            }
            MotionEvent.ACTION_UP -> {
                if (isBrightnessAdjusting) {
                    isBrightnessAdjusting = false
                    handler.postDelayed(hideBrightnessRunnable, 1000)
                } else {
                    val totalMove = abs(event.rawX - touchStartX) + abs(event.rawY - touchStartY)
                    if (totalMove < dp(15)) {
                        // 判定为纯粹单击！执行用户严格要求的逻辑：
                        onScreenClicked()
                    }
                }
            }
        }
        return true
    }

    private fun applyBrightness(value: Float) {
        val lp = window.attributes
        lp.screenBrightness = value
        window.attributes = lp

        brightnessHud.visibility = View.VISIBLE
        val percent = (value * 100).toInt()
        brightnessBar.progress = percent
        brightnessPercent.text = "$percent%"
        handler.removeCallbacks(hideBrightnessRunnable)
    }

    // 核心播控显示与暂停切换逻辑：
    // 单击屏幕：如果当前隐藏，则显示播控，绝不暂停；如果当前正在显示，再次点击才切换播放/暂停！
    private fun onScreenClicked() {
        if (!isControlsVisible) {
            showControls()
        } else {
            togglePlayPause()
        }
    }

    private fun showControls() {
        isControlsVisible = true
        controlsLayout.visibility = View.VISIBLE
        scheduleHideControls()
    }

    private fun hideControls() {
        if (isSeeking || isEpisodesDrawerVisible) return
        isControlsVisible = false
        controlsLayout.visibility = View.GONE
    }

    private fun scheduleHideControls() {
        handler.removeCallbacks(hideControlsRunnable)
        handler.postDelayed(hideControlsRunnable, 5000)
    }

    private fun togglePlayPause() {
        if (player == null) return
        if (player!!.isPlaying) {
            player!!.pause()
        } else {
            player!!.play()
        }
        updatePlayPauseState()
        scheduleHideControls()
    }

    private fun updatePlayPauseState() {
        val isPlaying = player?.isPlaying ?: false
        playPauseBtn.text = if (isPlaying) "❚❚" else "▶"
    }

    private fun toggleEpisodesDrawer() {
        isEpisodesDrawerVisible = !isEpisodesDrawerVisible
        episodesDrawer.visibility = if (isEpisodesDrawerVisible) View.VISIBLE else View.GONE
        if (isEpisodesDrawerVisible) {
            handler.removeCallbacks(hideControlsRunnable)
        } else {
            scheduleHideControls()
        }
    }

    private fun initExoPlayer() {
        val httpDataSourceFactory = DefaultHttpDataSource.Factory()
            .setAllowCrossProtocolRedirects(true)
            .setConnectTimeoutMs(15000)
            .setReadTimeoutMs(15000)

        val currentHeaders = episodesList.getOrNull(currentIndex)?.headers
        if (!currentHeaders.isNullOrEmpty()) {
            httpDataSourceFactory.setDefaultRequestProperties(currentHeaders)
        }

        val dataSourceFactory = DefaultDataSource.Factory(this, httpDataSourceFactory)
        val mediaSourceFactory = DefaultMediaSourceFactory(dataSourceFactory)

        player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(mediaSourceFactory)
            .build().apply {
                playWhenReady = true
                addListener(object : Player.Listener {
                    override fun onPlaybackStateChanged(state: Int) {
                        when (state) {
                            Player.STATE_BUFFERING -> loadingSpinner.visibility = View.VISIBLE
                            Player.STATE_READY -> {
                                loadingSpinner.visibility = View.GONE
                                updatePlayPauseState()
                                updateDuration()
                            }
                            Player.STATE_ENDED -> playNextEpisode()
                            Player.STATE_IDLE -> loadingSpinner.visibility = View.GONE
                        }
                    }
                    override fun onIsPlayingChanged(isPlaying: Boolean) {
                        updatePlayPauseState()
                    }
                    override fun onPlayerError(error: PlaybackException) {
                        loadingSpinner.visibility = View.GONE
                        Toast.makeText(
                            this@ExoPlayerActivity,
                            "播放遇到错误: ${error.errorCodeName}",
                            Toast.LENGTH_SHORT
                        ).show()
                    }
                })
            }
        playerView.player = player
    }

    private fun playEpisode(index: Int, seekMs: Long = 0L) {
        if (index < 0 || index >= episodesList.size) {
            Toast.makeText(this, "已经是最后一集", Toast.LENGTH_SHORT).show()
            return
        }
        currentIndex = index
        playCurrentEpisode(seekMs)
        populateEpisodesDrawer()
    }

    private fun playCurrentEpisode(seekMs: Long = 0L) {
        val ep = episodesList.getOrNull(currentIndex) ?: return
        titleText.text = "$dramaTitle · 第 ${ep.number} 集"

        if (ep.url.isEmpty()) {
            Toast.makeText(this, "第 ${ep.number} 集播放地址为空", Toast.LENGTH_SHORT).show()
            return
        }

        loadingSpinner.visibility = View.VISIBLE
        val mediaItem = MediaItem.fromUri(Uri.parse(ep.url))
        player?.setMediaItem(mediaItem)
        player?.prepare()
        if (seekMs > 0) {
            player?.seekTo(seekMs)
        }
        player?.play()
    }

    private fun playNextEpisode() {
        val next = currentIndex + 1
        if (next < episodesList.size) {
            playEpisode(next)
        } else {
            Toast.makeText(this, "全剧已播完", Toast.LENGTH_SHORT).show()
        }
    }

    private fun seekBy(deltaMs: Long) {
        val current = player?.currentPosition ?: 0L
        val duration = player?.duration ?: 0L
        val target = (current + deltaMs).coerceIn(0L, duration.coerceAtLeast(0L))
        player?.seekTo(target)
        showControls()
    }

    private fun updateProgress() {
        val p = player ?: return
        if (isSeeking) return
        val current = p.currentPosition.coerceAtLeast(0L)
        val duration = p.duration.coerceAtLeast(1L)

        timeCurrentText.text = formatTime(current)
        timeDurationText.text = formatTime(duration)

        val progress = ((current.toDouble() / duration.toDouble()) * 1000).toInt()
        seekBar.progress = progress
    }

    private fun updateDuration() {
        val duration = player?.duration?.coerceAtLeast(1L) ?: 1L
        timeDurationText.text = formatTime(duration)
    }

    private fun formatTime(millis: Long): String {
        val totalSeconds = (millis / 1000).coerceAtLeast(0L)
        val minutes = totalSeconds / 60
        val seconds = totalSeconds % 60
        return String.format(Locale.getDefault(), "%02d:%02d", minutes, seconds)
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        when (keyCode) {
            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER -> {
                onScreenClicked()
                return true
            }
            KeyEvent.KEYCODE_DPAD_LEFT -> {
                seekBy(-5000)
                return true
            }
            KeyEvent.KEYCODE_DPAD_RIGHT -> {
                seekBy(5000)
                return true
            }
            KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN -> {
                if (!isEpisodesDrawerVisible) {
                    toggleEpisodesDrawer()
                    return true
                }
            }
            KeyEvent.KEYCODE_BACK -> {
                if (isEpisodesDrawerVisible) {
                    toggleEpisodesDrawer()
                    return true
                }
                finishWithResult()
                return true
            }
        }
        return super.onKeyDown(keyCode, event)
    }

    private fun finishWithResult() {
        val resultIntent = Intent().apply {
            putExtra("index", currentIndex)
            putExtra("position", player?.currentPosition ?: 0L)
            putExtra("duration", player?.duration ?: 0L)
        }
        setResult(Activity.RESULT_OK, resultIntent)
        finish()
    }

    override fun onPause() {
        super.onPause()
        player?.pause()
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        player?.release()
        player = null
        super.onDestroy()
    }
}
