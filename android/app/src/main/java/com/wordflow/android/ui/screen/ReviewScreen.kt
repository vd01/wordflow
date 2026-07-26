package com.wordflow.android.ui.screen

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.*
import com.wordflow.android.ui.theme.*

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
        val sessionEasy: Int
    )

    fun buildQueue(app: WordFlowApp) {
        try {
            val words = app.store.getWords()
            val reviews = app.store.getReviews()
            val dailyNewRemaining = app.store.getDailyNewRemaining()
            val q = fsrs.getDueQueue(words, reviews, dailyNewRemaining)
            val counts = fsrs.getQueueCounts(words, reviews)

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
            errorMsg = "Failed to load review queue: ${e.message}"
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
            "easy" to (preview?.let { fsrs.formatInterval(it.easy.scheduledDays) } ?: "")
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
            if (c.state == STATE_NEW) app.store.incrementDailyNewCount()
        } catch (e: Exception) {
            errorMsg = "Rating failed: ${e.message}"
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
            if (last.card.state == STATE_NEW) app.store.decrementDailyNewCount()
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

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("Remaining: ${vm.remaining}  Reviewed: ${vm.reviewed}")
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Back") }
                },
                actions = {
                    IconButton(onClick = onGoSettings) { Icon(Icons.Default.Settings, "Settings") }
                }
            )
        }
    ) { padding ->
        if (vm.emptyState) {
            // Session complete screen
            Column(
                modifier = Modifier.fillMaxSize().padding(padding).padding(32.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center
            ) {
                Icon(Icons.Default.CheckCircle, null, modifier = Modifier.size(64.dp),
                    tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.height(16.dp))
                Text("Session Complete", style = MaterialTheme.typography.headlineMedium)
                Text("You reviewed ${vm.reviewed} cards", style = MaterialTheme.typography.bodyLarge)
                Spacer(Modifier.height(24.dp))

                Row(horizontalArrangement = Arrangement.spacedBy(24.dp)) {
                    StatColumn("Again", vm.sessionAgain, AgainColor)
                    StatColumn("Hard", vm.sessionHard, HardColor)
                    StatColumn("Good", vm.sessionGood, GoodColor)
                    StatColumn("Easy", vm.sessionEasy, EasyColor)
                }

                Spacer(Modifier.height(16.dp))
                val hint = when {
                    vm.remainingDue > 0 -> "${vm.remainingDue} cards still due"
                    else -> "All caught up — see you next time!"
                }
                Text(hint, style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)

                Spacer(Modifier.height(24.dp))
                if (vm.remainingDue > 0) {
                    Button(onClick = { vm.buildQueue(app) }, modifier = Modifier.fillMaxWidth()) {
                        Text("Continue Review")
                    }
                }
                OutlinedButton(onClick = onGoSettings, modifier = Modifier.fillMaxWidth()) {
                    Icon(Icons.Default.Settings, null); Spacer(Modifier.width(8.dp)); Text("Settings")
                }
            }
        } else if (vm.entry != null) {
            // Card area
            Column(
                modifier = Modifier.fillMaxSize().padding(padding)
            ) {
                // Scrollable card content
                Column(
                    modifier = Modifier
                        .weight(1f)
                        .verticalScroll(rememberScrollState())
                        .padding(16.dp)
                        .clickable { if (!vm.revealed) vm.reveal() },
                    horizontalAlignment = Alignment.CenterHorizontally
                ) {
                    // Word
                    Text(vm.entry!!.word, style = MaterialTheme.typography.headlineLarge,
                        fontWeight = FontWeight.Bold)
                    // Phonetic + speaker
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        vm.parsedResult?.phonetic?.let {
                            Text("/$it/", style = MaterialTheme.typography.titleMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }

                    // Reveal hint
                    if (!vm.revealed) {
                        Spacer(Modifier.height(32.dp))
                        Text("Tap to reveal answer", color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.bodyMedium)
                    }

                    // Back side content
                    if (vm.revealed) {
                        Spacer(Modifier.height(16.dp))
                        Divider()
                        Spacer(Modifier.height(12.dp))

                        val pr = vm.parsedResult ?: return@Column

                        // Badges
                        if (pr.tag.isNotBlank() || pr.collins != null || pr.oxford != null) {
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                if (pr.tag.isNotBlank()) BadgeText(pr.tag, MaterialTheme.colorScheme.tertiaryContainer)
                                if (pr.collins != null) BadgeText("★${pr.collins}", MaterialTheme.colorScheme.primaryContainer)
                                if (pr.oxford != null) BadgeText("Oxford", MaterialTheme.colorScheme.secondaryContainer)
                            }
                            Spacer(Modifier.height(8.dp))
                        }

                        // Translation
                        if (pr.translation.isNotBlank()) {
                            SectionLabel("释义"); SectionText(pr.translation); Spacer(Modifier.height(8.dp))
                        }
                        // Definition
                        if (pr.definition.isNotBlank()) {
                            SectionLabel("Definition"); SectionText(pr.definition); Spacer(Modifier.height(8.dp))
                        }
                        // LLM Definitions
                        if (pr.definitions.isNotEmpty()) {
                            SectionLabel("详细释义")
                            pr.definitions.forEach { def ->
                                if (def.pos.isNotBlank()) Text(def.pos, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                                if (def.meaning.isNotBlank()) Text(def.meaning, fontSize = 14.sp)
                                if (def.englishExample.isNotBlank()) Text("📝 ${def.englishExample}", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                if (def.chineseExample.isNotBlank()) Text("💡 ${def.chineseExample}", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                Spacer(Modifier.height(6.dp))
                            }
                        }
                        // Memory tips
                        if (pr.memoryTips.isNotBlank()) {
                            SectionLabel("🧠 记忆技巧"); SectionText(pr.memoryTips); Spacer(Modifier.height(8.dp))
                        }
                        // Synonyms
                        if (pr.synonyms.isNotBlank()) {
                            SectionLabel("📌 近义词"); SectionText(pr.synonyms); Spacer(Modifier.height(8.dp))
                        }
                        // Antonyms
                        if (pr.antonyms.isNotBlank()) {
                            SectionLabel("🚫 反义词"); SectionText(pr.antonyms); Spacer(Modifier.height(8.dp))
                        }
                        // Etymology
                        if (pr.etymology.isNotBlank()) {
                            SectionLabel("📚 词源"); SectionText(pr.etymology); Spacer(Modifier.height(8.dp))
                        }
                        // Exchange
                        if (pr.exchange.isNotBlank()) {
                            SectionLabel("🔄 词形变化"); SectionText(pr.exchange); Spacer(Modifier.height(8.dp))
                        }

                        Spacer(Modifier.height(80.dp)) // space for bottom bar
                    }
                }

                // Bottom rating bar (shown when revealed)
                if (vm.revealed) {
                    Surface(shadowElevation = 8.dp) {
                        Column(modifier = Modifier.padding(horizontal = 8.dp, vertical = 8.dp)) {
                            if (vm.undoCount > 0) {
                                TextButton(onClick = { vm.undo(app) }) {
                                    Text("↩ Undo")
                                }
                            }
                            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                RatingButton("Again", vm.intervals["again"], AgainColor) { vm.rate(RATING_AGAIN, app) }
                                RatingButton("Hard", vm.intervals["hard"], HardColor) { vm.rate(RATING_HARD, app) }
                                RatingButton("Good", vm.intervals["good"], GoodColor) { vm.rate(RATING_GOOD, app) }
                                RatingButton("Easy", vm.intervals["easy"], EasyColor) { vm.rate(RATING_EASY, app) }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun StatColumn(label: String, count: Int, color: androidx.compose.ui.graphics.Color) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text("$count", style = MaterialTheme.typography.headlineSmall, color = color, fontWeight = FontWeight.Bold)
        Text(label, style = MaterialTheme.typography.bodySmall)
    }
}

@Composable
private fun BadgeText(text: String, color: androidx.compose.ui.graphics.Color) {
    Surface(color = color, shape = MaterialTheme.shapes.small) {
        Text(text, modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
            style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
private fun SectionLabel(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.primary)
    Spacer(Modifier.height(2.dp))
}

@Composable
private fun SectionText(text: String) {
    Text(text, style = MaterialTheme.typography.bodyMedium)
}

@Composable
private fun RowScope.RatingButton(label: String, interval: String?, color: androidx.compose.ui.graphics.Color, onClick: () -> Unit) {
    OutlinedButton(
        onClick = onClick,
        modifier = Modifier.weight(1f),
        colors = ButtonDefaults.outlinedButtonColors(contentColor = color)
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(label, fontSize = 13.sp, fontWeight = FontWeight.Bold)
            Text(interval ?: "", fontSize = 11.sp)
        }
    }
}

