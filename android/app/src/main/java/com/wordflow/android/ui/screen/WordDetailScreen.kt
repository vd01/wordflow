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
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Event
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.ui.components.AudioButton
import com.wordflow.android.ui.components.rememberTtsSpeaker
import com.wordflow.android.ui.components.DefinitionBlock
import com.wordflow.android.ui.components.MetaBadges
import com.wordflow.android.ui.components.SectionBlock
import com.wordflow.android.ui.components.SectionText
import com.wordflow.android.ui.components.StateBadge
import com.wordflow.android.ui.components.formatPhonetic
import com.wordflow.android.ui.theme.Dimens
import kotlin.math.ceil

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WordDetailScreen(wordId: String, onBack: () -> Unit) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val fsrs = remember { FsrsEngine() }
    val speaker = rememberTtsSpeaker(app.store)

    val entry = remember { app.store.getWord(wordId) }
    val parsedResult = entry?.let { app.store.parseResult(it) }
    var card by remember { mutableStateOf(app.store.getReview(wordId)) }

    val state = card?.state ?: 0
    val dueInfo = card?.let {
        if (it.due <= 0) "已到期" else {
            val diffMs = it.due - System.currentTimeMillis()
            when {
                diffMs <= 0 -> "已到期"
                else -> {
                    val diffDays = ceil(diffMs / (1000.0 * 60 * 60 * 24)).toInt()
                    if (diffDays == 1) "明天到期" else "$diffDays 天后到期"
                }
            }
        }
    } ?: ""
    val intervalStr = card?.let { if (it.scheduledDays > 0) fsrs.formatInterval(it.scheduledDays) else "" } ?: ""

    Scaffold(
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0.dp, 0.dp, 0.dp, 0.dp),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            TopAppBar(
                title = { Text(entry?.word ?: "单词详情") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回") }
                },
                actions = {
                    if (parsedResult != null) AudioButton(speaker = speaker, text = parsedResult.word)
                },
            )
        },
    ) { padding ->
        if (entry == null) {
            Column(Modifier.fillMaxSize().padding(padding).padding(32.dp)) {
                Text("未找到单词", color = MaterialTheme.colorScheme.error)
            }
            return@Scaffold
        }
        val pr = parsedResult ?: return@Scaffold

        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(Dimens.screenPadding),
            verticalArrangement = Arrangement.spacedBy(Dimens.sm),
        ) {
            // Header
            Text(entry.word, style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                if (pr.phonetic.isNotBlank()) {
                    Text(formatPhonetic(pr.phonetic), style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                StateBadge(state)
            }
            if (dueInfo.isNotBlank()) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    Icon(Icons.Default.Event, contentDescription = null, modifier = Modifier.size(16.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                    val text = if (intervalStr.isNotBlank()) "$dueInfo · 间隔 $intervalStr" else dueInfo
                    Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }

            Spacer(Modifier.height(8.dp))
            MetaBadges(tag = pr.tag, collins = pr.collins, oxford = pr.oxford)
            Spacer(Modifier.height(8.dp))

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

            Spacer(Modifier.height(Dimens.lg))
            // Actions
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                OutlinedButton(onClick = {
                    app.store.removeReview(wordId)
                    card = null
                }) {
                    Icon(Icons.Default.Refresh, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                    Text("重置进度")
                }
            }
            Spacer(Modifier.height(Dimens.xl))
        }
    }
}
