package com.wordflow.android.ui.screen

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.animation.core.tween
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Inbox
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Undo
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsCard
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.data.ParsedResult
import com.wordflow.android.data.RATING_AGAIN
import com.wordflow.android.data.RATING_EASY
import com.wordflow.android.data.RATING_GOOD
import com.wordflow.android.data.RATING_HARD
import com.wordflow.android.data.SyncEntry
import com.wordflow.android.ui.components.AudioButton
import com.wordflow.android.ui.components.DefinitionBlock
import com.wordflow.android.ui.components.FlashCard
import com.wordflow.android.ui.components.MetaBadges
import com.wordflow.android.ui.components.RatingButtonRow
import com.wordflow.android.ui.components.SectionBlock
import com.wordflow.android.ui.components.SectionText
import com.wordflow.android.ui.components.StatusBanner
import com.wordflow.android.ui.components.EmptyState
import com.wordflow.android.ui.components.StatusKind
import com.wordflow.android.ui.components.formatPhonetic
import com.wordflow.android.ui.theme.Dimens
import com.wordflow.android.ui.theme.RatingAgain
import com.wordflow.android.ui.theme.RatingEasy
import com.wordflow.android.ui.theme.RatingGood
import com.wordflow.android.ui.theme.RatingHard

class ReviewViewModel : ViewModel() {
    var queue by mutableStateOf<List<String>>(emptyList())
    var currentIndex by mutableStateOf(0)
    var remaining by mutableStateOf(0)
    var reviewed by mutableStateOf(0)
    var sessionAgain by mutableStateOf(0)
    var sessionHard by mutableStateOf(0)
    var sessionGood by mutableStateOf(0)
    var sessionEasy by mutableStateOf(0)
    var remainingDue by mutableStateOf(0)
    var entry by mutableStateOf<SyncEntry?>(null)
    var card by mutableStateOf<FsrsCard?>(null)
    var parsedResult by mutableStateOf<ParsedResult?>(null)
    var revealed by mutableStateOf(false)
    var intervals by mutableStateOf<Map<String, String>>(emptyMap())
    var emptyState by mutableStateOf(false)
    var undoCount by mutableStateOf(0)
    var errorMsg by mutableStateOf("")

    private val _undoStack = mutableListOf<UndoState>()
    private val fsrs = FsrsEngine()

    data class UndoState(
        val index: Int,
        val card: FsrsCard,
        val reviewed: Int,
        val sessionAgain: Int,
        val sessionHard: Int,
        val sessionGood: Int,
        val sessionEasy: Int,
    )

    fun buildQueue(app: WordFlowApp) {
        try {
            val words = app.store.getWords()
            val reviews = app.store.getReviews()
            val dailyNewRemaining = app.store.getDailyNewRemaining()
            val q = fsrs.getDueQueue(words, reviews, dailyNewRemaining)

            queue = q
            currentIndex = 0
            remaining = q.size
            reviewed = 0
            sessionAgain = 0
            sessionHard = 0
            sessionGood = 0
            sessionEasy = 0
            remainingDue = 0
            emptyState = q.isEmpty()
            _undoStack.clear()
            errorMsg = ""

            if (q.isNotEmpty()) showCard(0, app) else {
                entry = null; card = null; parsedResult = null; revealed = false
            }
        } catch (e: Exception) {
            errorMsg = "加载复习队列失败：${e.message}"
        }
    }

    private fun showCard(index: Int, app: WordFlowApp) {
        if (index >= queue.size) { showDoneScreen(app); return }
        val id = queue[index]
        val e = app.store.getWord(id)
        if (e == null) { showCard(index + 1, app); return }

        val pr = app.store.parseResult(e)
        var c = app.store.getReview(id)
        if (c == null) c = fsrs.createCard(id)

        val preview = try { fsrs.previewIntervals(c) } catch (_: Exception) { null }
        val ints: Map<String, String> = mapOf(
            "again" to (preview?.let { fsrs.formatInterval(it.again.scheduledDays) } ?: ""),
            "hard" to (preview?.let { fsrs.formatInterval(it.hard.scheduledDays) } ?: ""),
            "good" to (preview?.let { fsrs.formatInterval(it.good.scheduledDays) } ?: ""),
            "easy" to (preview?.let { fsrs.formatInterval(it.easy.scheduledDays) } ?: ""),
        )

        entry = e; card = c; parsedResult = pr; revealed = false; intervals = ints
        currentIndex = index; remaining = queue.size - index; errorMsg = ""
    }

    private fun showDoneScreen(app: WordFlowApp) {
        val words = app.store.getWords()
        val reviews = app.store.getReviews()
        remainingDue = fsrs.getQueueCounts(words, reviews).total
        emptyState = true; entry = null; card = null; parsedResult = null
    }

    fun reveal() { revealed = true }

    fun rate(rating: Int, app: WordFlowApp) {
        val c = card ?: return
        if (rating !in 1..4) return

        _undoStack.add(UndoState(currentIndex, c.copy(), reviewed, sessionAgain, sessionHard, sessionGood, sessionEasy))
        undoCount = _undoStack.size

        try {
            val result = fsrs.rateCard(c, rating)
            app.store.saveReview(c.id.ifBlank { entry?.id ?: "" }, result.card)
            if (c.state == com.wordflow.android.data.STATE_NEW) app.store.incrementDailyNewCount()
        } catch (e: Exception) {
            errorMsg = "评分失败：${e.message}"
            _undoStack.removeLast()
            undoCount = _undoStack.size
            return
        }

        when (rating) {
            RATING_AGAIN -> sessionAgain++
            RATING_HARD -> sessionHard++
            RATING_GOOD -> sessionGood++
            RATING_EASY -> sessionEasy++
        }
        reviewed++

        val nextIndex = currentIndex + 1
        remaining = queue.size - nextIndex
        if (nextIndex < queue.size) showCard(nextIndex, app) else showDoneScreen(app)
    }

    fun undo(app: WordFlowApp) {
        if (_undoStack.isEmpty()) return
        val last = _undoStack.removeLast()
        try {
            app.store.saveReview(last.card.id, last.card)
            if (last.card.state == com.wordflow.android.data.STATE_NEW) app.store.decrementDailyNewCount()
        } catch (_: Exception) {}
        reviewed = last.reviewed; sessionAgain = last.sessionAgain
        sessionHard = last.sessionHard; sessionGood = last.sessionGood
        sessionEasy = last.sessionEasy; currentIndex = last.index
        remaining = queue.size - last.index
        undoCount = _undoStack.size
        showCard(last.index, app)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReviewScreen(onBack: () -> Unit, onGoSettings: () -> Unit) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val vm: ReviewViewModel = viewModel()

    LaunchedEffect(Unit) { vm.buildQueue(app) }

    val total = vm.queue.size
    val progress = if (total > 0) vm.reviewed.toFloat() / total else 0f

    Scaffold(
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0.dp, 0.dp, 0.dp, 0.dp),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            Column {
                TopAppBar(
                    title = { Text("复习") },
                    navigationIcon = {
                        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回") }
                    },
                    actions = {
                        if (vm.undoCount > 0) {
                            IconButton(onClick = { vm.undo(app) }) { Icon(Icons.Default.Undo, contentDescription = "撤销") }
                        }
                        IconButton(onClick = onGoSettings) { Icon(Icons.Default.Settings, contentDescription = "设置") }
                    },
                )
                LinearProgressIndicator(
                    progress = { progress },
                    modifier = Modifier.fillMaxWidth().height(3.dp),
                )
            }
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            if (vm.errorMsg.isNotBlank()) {
                StatusBanner(text = vm.errorMsg, kind = StatusKind.ERROR, modifier = Modifier.padding(16.dp))
            }

            when {
                vm.emptyState -> if (vm.reviewed > 0) SessionComplete(vm, app, onBack) else ReviewEmpty(onBack)
                vm.entry != null -> {
                    if (total > 0) {
                        Text(
                            "第 ${vm.currentIndex + 1} / $total 张",
                            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                            textAlign = TextAlign.Center,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Box(
                        modifier = Modifier.weight(1f).fillMaxWidth().padding(horizontal = Dimens.screenPadding),
                        contentAlignment = Alignment.TopCenter,
                    ) {
                        AnimatedContent(
                            targetState = vm.revealed,
                            transitionSpec = { fadeIn(tween(200)) togetherWith fadeOut(tween(200)) },
                            label = "reveal",
                        ) { revealed ->
                            if (revealed) BackCard(vm) else FrontCard(vm)
                        }
                    }
                    AnimatedVisibility(visible = vm.revealed) {
                        Column(Modifier.padding(horizontal = Dimens.screenPadding, vertical = Dimens.md)) {
                            RatingButtonRow(intervals = vm.intervals, onRate = { vm.rate(it, app) })
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun FrontCard(vm: ReviewViewModel) {
    val entry = vm.entry ?: return
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        FlashCard(onClick = { vm.reveal() }) {
            Column(
                modifier = Modifier.fillMaxWidth().padding(vertical = Dimens.xl),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(Dimens.sm),
            ) {
                Text(entry.word, style = MaterialTheme.typography.displaySmall, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center)
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    vm.parsedResult?.phonetic?.takeIf { it.isNotBlank() }?.let {
                        Text(formatPhonetic(it), style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    AudioButton(text = entry.word)
                }
                Spacer(Modifier.height(Dimens.xl))
                Text("轻点卡片查看答案", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@Composable
private fun BackCard(vm: ReviewViewModel) {
    val entry = vm.entry ?: return
    val pr = vm.parsedResult
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        FlashCard(onClick = null) {
            Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Dimens.sm)) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(entry.word, style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
                    AudioButton(text = entry.word)
                }
                pr?.phonetic?.takeIf { it.isNotBlank() }?.let {
                    Text(formatPhonetic(it), style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Spacer(Modifier.height(4.dp))
                if (pr != null) MetaBadges(tag = pr.tag, collins = pr.collins, oxford = pr.oxford)
                Spacer(Modifier.height(4.dp))

                if (pr != null) {
                    if (pr.translation.isNotBlank()) {
                        SectionBlock(label = "释义") { SectionText(pr.translation) }
                    }
                    if (pr.definition.isNotBlank()) {
                        SectionBlock(label = "英文释义") { SectionText(pr.definition) }
                    }
                    if (pr.definitions.isNotEmpty()) {
                        SectionBlock(label = "详细释义") {
                            pr.definitions.forEach { def ->
                                DefinitionBlock(
                                    pos = def.pos,
                                    meaning = def.meaning,
                                    englishExample = def.englishExample,
                                    chineseExample = def.chineseExample,
                                )
                            }
                        }
                    }
                    if (pr.memoryTips.isNotBlank()) {
                        SectionBlock(label = "记忆技巧") { SectionText(pr.memoryTips) }
                    }
                    if (pr.synonyms.isNotBlank()) {
                        SectionBlock(label = "近义词") { SectionText(pr.synonyms) }
                    }
                    if (pr.antonyms.isNotBlank()) {
                        SectionBlock(label = "反义词") { SectionText(pr.antonyms) }
                    }
                    if (pr.etymology.isNotBlank()) {
                        SectionBlock(label = "词源") { SectionText(pr.etymology) }
                    }
                    if (pr.exchange.isNotBlank()) {
                        SectionBlock(label = "词形变化") { SectionText(pr.exchange) }
                    }
                }
            }
        }
        Spacer(Modifier.height(Dimens.lg))
    }
}

@Composable
private fun SessionComplete(vm: ReviewViewModel, app: WordFlowApp, onBack: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Icon(Icons.Default.CheckCircle, contentDescription = null, modifier = Modifier.size(72.dp), tint = MaterialTheme.colorScheme.primary)
        Spacer(Modifier.height(16.dp))
        Text("完成！", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
        Text("本次复习 ${vm.reviewed} 张", style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(24.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(24.dp)) {
            StatColumn("Again", vm.sessionAgain, RatingAgain)
            StatColumn("Hard", vm.sessionHard, RatingHard)
            StatColumn("Good", vm.sessionGood, RatingGood)
            StatColumn("Easy", vm.sessionEasy, RatingEasy)
        }
        Spacer(Modifier.height(16.dp))
        val hint = if (vm.remainingDue > 0) "还有 ${vm.remainingDue} 张待复习" else "全部完成，下次见！"
        Text(hint, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(24.dp))
        if (vm.remainingDue > 0) {
            Button(
                onClick = { vm.buildQueue(app) },
                modifier = Modifier.fillMaxWidth(),
                shape = MaterialTheme.shapes.medium,
            ) { Text("继续复习") }
            Spacer(Modifier.height(8.dp))
        }
        OutlinedButton(
            onClick = onBack,
            modifier = Modifier.fillMaxWidth(),
            shape = MaterialTheme.shapes.medium,
        ) { Text("返回首页") }
    }
}

@Composable
private fun StatColumn(label: String, count: Int, color: Color) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text("$count", style = MaterialTheme.typography.headlineSmall, color = color, fontWeight = FontWeight.Bold)
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
        }

        @Composable
        private fun ReviewEmpty(onBack: () -> Unit) {
    EmptyState(
        icon = Icons.Default.Inbox,
        title = "暂无待复习单词",
        subtitle = "全部完成，稍后再来看看吧",
        action = {
            OutlinedButton(onClick = onBack, modifier = Modifier.fillMaxWidth(), shape = MaterialTheme.shapes.medium) {
                Text("返回首页")
            }
        },
        modifier = Modifier.fillMaxSize(),
    )
}
