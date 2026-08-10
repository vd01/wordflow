package com.wordflow.android.ui.screen

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowForward
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.School
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.data.SyncClient
import com.wordflow.android.ui.components.StateDot
import com.wordflow.android.ui.components.StatTile
import com.wordflow.android.ui.components.StatusBanner
import com.wordflow.android.ui.components.StatusKind
import com.wordflow.android.ui.theme.Dimens
import com.wordflow.android.data.STATE_LEARNING
import com.wordflow.android.data.STATE_NEW
import com.wordflow.android.data.STATE_REVIEW
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

data class HomeStatus(val text: String, val kind: StatusKind)

class HomeViewModel : ViewModel() {
    var wordCount by mutableStateOf(0)
    var dueCount by mutableStateOf(0)
    var newCount by mutableStateOf(0)
    var learningCount by mutableStateOf(0)
    var reviewCount by mutableStateOf(0)
    var relearningCount by mutableStateOf(0)
    var lastSyncDisplay by mutableStateOf("从未同步")
    var dailyNewCount by mutableStateOf(0)
    var dailyLimit by mutableStateOf(0)
    var status by mutableStateOf<HomeStatus?>(null)
    var isSyncing by mutableStateOf(false)
        private set

    private val client = SyncClient()
    private val fsrs = FsrsEngine()
    private val syncMutex = Mutex()

    fun refresh(app: WordFlowApp) {
        val store = app.store
        val words = store.getWords()
        val reviews = store.getReviews()
        val dailyNewRemaining = store.getDailyNewRemaining()
        val dueCounts = fsrs.getQueueCounts(words, reviews, dailyNewRemaining)
        val totalCounts = fsrs.getTotalCounts(words, reviews)
        wordCount = words.size
        dueCount = dueCounts.total
        newCount = totalCounts.new
        learningCount = totalCounts.learning
        reviewCount = totalCounts.review
        relearningCount = totalCounts.relearning
        lastSyncDisplay = formatSyncTime(store.lastSync)
        dailyLimit = store.dailyLimit
        dailyNewCount = store.getDailyCount().newCount
    }

    fun sync(app: WordFlowApp) {
        val store = app.store
        if (!store.isLoggedIn) return
        viewModelScope.launch {
            syncMutex.withLock {
                if (isSyncing) return@launch
                isSyncing = true
                status = null
                try {
                    val since = store.lastSync
                    // Pull words + review cards from server
                    val (wordRes, reviewRes) = withContext(Dispatchers.IO) {
                        val wr = client.pull(store.serverAddr, store.token, since)
                        val rr = client.pullReviews(store.serverAddr, store.token, since)
                        Pair(wr, rr)
                    }
                    store.mergePulled(wordRes.entries, wordRes.serverNow, since == 0L)
                    store.mergePulledReviews(reviewRes.cards)
                    // Push local review cards to server
                    withContext(Dispatchers.IO) {
                        val localCards = store.getAllReviewCards()
                        if (localCards.isNotEmpty()) {
                            client.pushReviews(store.serverAddr, store.token, localCards)
                        }
                    }
                    refresh(app)
                } catch (e: Exception) {
                    status = HomeStatus("同步失败：${e.message ?: e.javaClass.simpleName}", StatusKind.ERROR)
                } finally {
                    isSyncing = false
                }
            }
        }
    }

    private fun formatSyncTime(ts: Long): String {
        if (ts <= 0) return "从未同步"
        val now = System.currentTimeMillis() / 1000
        val diff = now - ts
        return when {
            diff < 60 -> "刚刚同步"
            diff < 3600 -> "${diff / 60} 分钟前同步"
            diff < 86400 -> "${diff / 3600} 小时前同步"
            diff < 604800 -> "${diff / 86400} 天前同步"
            else -> {
                val d = java.util.Date(ts * 1000)
                val sdf = java.text.SimpleDateFormat("yyyy-MM-dd HH:mm", java.util.Locale.getDefault())
                sdf.format(d)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    onNavigateToReview: () -> Unit,
    onNavigateToSettings: () -> Unit,
    onNavigateToWordList: () -> Unit,
) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val vm: HomeViewModel = viewModel()
    val lifecycleOwner = LocalLifecycleOwner.current

    // Sync every time the screen becomes visible (navigate back, reopen app, etc.)
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                vm.refresh(app)
                if (app.store.isLoggedIn && !vm.isSyncing) vm.sync(app)
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    Scaffold(
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0.dp, 0.dp, 0.dp, 0.dp),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            TopAppBar(
                title = { Text("查词温故") },
                actions = {
                    IconButton(onClick = onNavigateToSettings) {
                        Icon(Icons.Default.Settings, contentDescription = "设置")
                    }
                },
            )
        },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = vm.isSyncing,
            onRefresh = { vm.sync(app) },
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(Dimens.screenPadding),
            verticalArrangement = Arrangement.spacedBy(Dimens.lg),
        ) {
            // Hero card — due today is the centerpiece
            Surface(
                color = MaterialTheme.colorScheme.primaryContainer,
                contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
                shape = MaterialTheme.shapes.large,
            ) {
                Column(Modifier.padding(20.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column {
                            Text("今日待复习", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                            Text("DUE TODAY", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.7f))
                        }
                        Icon(Icons.Default.School, contentDescription = null, modifier = Modifier.size(32.dp), tint = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.8f))
                    }
                    Spacer(Modifier.height(8.dp))
                    Text("${vm.dueCount}", style = MaterialTheme.typography.displayLarge, fontWeight = FontWeight.Bold)
                    Text("个单词待复习", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.85f))
                    Spacer(Modifier.height(16.dp))
                    if (vm.dueCount > 0) {
                        Button(
                            onClick = onNavigateToReview,
                            modifier = Modifier.fillMaxWidth(),
                            shape = MaterialTheme.shapes.medium,
                        ) {
                            Text("开始复习")
                            Spacer(Modifier.width(8.dp))
                            Icon(Icons.Default.ArrowForward, contentDescription = null)
                        }
                    } else {
                        Surface(
                            color = MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.08f),
                            shape = MaterialTheme.shapes.medium,
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Text(
                                "暂无待复习，休息一下吧",
                                modifier = Modifier.padding(14.dp),
                                style = MaterialTheme.typography.bodyMedium,
                                textAlign = TextAlign.Center,
                            )
                        }
                    }
                }
            }

            // Sync status line
            if (vm.isSyncing) {
                StatusBanner(text = "正在同步…", kind = StatusKind.LOADING)
            } else {
                Text("已${vm.lastSyncDisplay}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }

            // Stats
            Row(horizontalArrangement = Arrangement.spacedBy(Dimens.md)) {
                StatTile("${vm.wordCount}", "单词总数", Modifier.weight(1f))
                val daily = if (vm.dailyLimit > 0) "${vm.dailyNewCount}/${vm.dailyLimit}" else "${vm.dailyNewCount}"
                StatTile(daily, "今日新词", Modifier.weight(1f))
            }

            // Composition card — fills the gap, shows review-queue breakdown
            Surface(
                color = MaterialTheme.colorScheme.surface,
                tonalElevation = 1.dp,
                shape = MaterialTheme.shapes.large,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Column(Modifier.padding(Dimens.cardPadding)) {
                    Text("词库构成", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                    Spacer(Modifier.height(12.dp))
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        CompItem(STATE_NEW, "新词", vm.newCount)
                        CompItem(STATE_LEARNING, "学习中", vm.learningCount + vm.relearningCount)
                        CompItem(STATE_REVIEW, "复习", vm.reviewCount)
                    }
                }
            }

            // Word list shortcut
            OutlinedButton(
                onClick = onNavigateToWordList,
                modifier = Modifier.fillMaxWidth(),
                shape = MaterialTheme.shapes.medium,
            ) {
                Icon(Icons.AutoMirrored.Filled.List, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("词库 (${vm.wordCount})")
            }

            vm.status?.let { StatusBanner(text = it.text, kind = it.kind) }

            Spacer(Modifier.height(Dimens.lg))
        }
        }
    }
}

@Composable
private fun CompItem(state: Int, label: String, count: Int) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
        modifier = Modifier,
    ) {
        StateDot(state)
        Spacer(Modifier.height(4.dp))
        Text("$count", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onSurface)
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

