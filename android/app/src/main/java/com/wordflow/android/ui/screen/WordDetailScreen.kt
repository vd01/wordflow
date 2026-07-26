package com.wordflow.android.ui.screen

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.data.STATE_LABELS

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WordDetailScreen(wordId: String, onBack: () -> Unit) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val fsrs = remember { FsrsEngine() }

    val entry = app.store.getWord(wordId)
    val parsedResult = entry?.let { app.store.parseResult(it) }
    val card = app.store.getReview(wordId)

    val stateLabel = STATE_LABELS[card?.state ?: 0] ?: "New"
    val nextDue = card?.let {
        if (it.due <= 0) ""
        else {
            val diffMs = it.due - System.currentTimeMillis()
            when {
                diffMs <= 0 -> "Due now"
                else -> {
                    val diffDays = Math.ceil(diffMs / (1000.0 * 60 * 60 * 24)).toInt()
                    if (diffDays == 1) "Due tomorrow" else "Due in $diffDays days"
                }
            }
        }
    } ?: ""
    val interval = card?.let {
        if (it.scheduledDays > 0) fsrs.formatInterval(it.scheduledDays) else ""
    } ?: ""

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(entry?.word ?: "Word Detail") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Back") }
                }
            )
        }
    ) { padding ->
        if (entry == null) {
            Column(modifier = Modifier.fillMaxSize().padding(padding).padding(32.dp)) {
                Text("Word not found", color = MaterialTheme.colorScheme.error)
            }
            return@Scaffold
        }

        Column(modifier = Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(16.dp)) {
            // Word header
            Text(entry.word, style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
            Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                parsedResult?.phonetic?.let {
                    Text("/$it/", style = MaterialTheme.typography.titleMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Spacer(Modifier.width(12.dp))
                Surface(color = MaterialTheme.colorScheme.secondaryContainer, shape = MaterialTheme.shapes.small) {
                    Text(stateLabel, modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
                        style = MaterialTheme.typography.labelSmall)
                }
            }
            if (nextDue.isNotBlank()) {
                Spacer(Modifier.height(4.dp))
                Text("📅 $nextDue${if (interval.isNotBlank()) "  Interval: $interval" else ""}",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }

            Spacer(Modifier.height(16.dp))
            Divider()
            Spacer(Modifier.height(12.dp))

            val pr = parsedResult ?: return@Column

            // Badges
            if (pr.tag.isNotBlank() || pr.collins != null || pr.oxford != null) {
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    if (pr.tag.isNotBlank()) Surface(color = MaterialTheme.colorScheme.tertiaryContainer, shape = MaterialTheme.shapes.small) {
                        Text(pr.tag, modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
                    }
                    if (pr.collins != null) Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = MaterialTheme.shapes.small) {
                        Text("★${pr.collins}", modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
                    }
                    if (pr.oxford != null) Surface(color = MaterialTheme.colorScheme.secondaryContainer, shape = MaterialTheme.shapes.small) {
                        Text("Oxford", modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
                    }
                }
                Spacer(Modifier.height(8.dp))
            }

            if (pr.translation.isNotBlank()) { Section("释义", pr.translation) }
            if (pr.definition.isNotBlank()) { Section("Definition", pr.definition) }
            if (pr.definitions.isNotEmpty()) {
                Text("详细释义", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.primary)
                pr.definitions.forEach { def ->
                    if (def.pos.isNotBlank()) Text(def.pos, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                    if (def.meaning.isNotBlank()) Text(def.meaning, fontSize = 14.sp)
                    if (def.englishExample.isNotBlank()) Text("📝 ${def.englishExample}", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    if (def.chineseExample.isNotBlank()) Text("💡 ${def.chineseExample}", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(Modifier.height(6.dp))
                }
            }
            if (pr.memoryTips.isNotBlank()) { Section("🧠 记忆技巧", pr.memoryTips) }
            if (pr.synonyms.isNotBlank()) { Section("📌 近义词", pr.synonyms) }
            if (pr.antonyms.isNotBlank()) { Section("🚫 反义词", pr.antonyms) }
            if (pr.etymology.isNotBlank()) { Section("📚 词源", pr.etymology) }
            if (pr.exchange.isNotBlank()) { Section("🔄 词形变化", pr.exchange) }
        }
    }
}

@Composable
private fun Section(label: String, text: String) {
    Spacer(Modifier.height(8.dp))
    Text(label, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.primary)
    Text(text, style = MaterialTheme.typography.bodyMedium)
}
