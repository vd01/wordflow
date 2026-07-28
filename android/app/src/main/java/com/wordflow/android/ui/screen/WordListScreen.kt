package com.wordflow.android.ui.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.data.STATE_LEARNING
import com.wordflow.android.data.STATE_NEW
import com.wordflow.android.data.STATE_RELEARNING
import com.wordflow.android.data.STATE_REVIEW
import com.wordflow.android.ui.components.EmptyState
import com.wordflow.android.ui.components.StateBadge
import com.wordflow.android.ui.components.StateDot
import com.wordflow.android.ui.components.formatPhonetic

class WordListViewModel : ViewModel() {
    var words by mutableStateOf<List<WordItem>>(emptyList())
    var search by mutableStateOf("")
    var filtered by mutableStateOf<List<WordItem>>(emptyList())
    var counts by mutableStateOf(FsrsEngine.QueueCounts(0, 0, 0, 0, 0))

    data class WordItem(
        val id: String,
        val word: String,
        val state: Int,
        val phonetic: String,
        val translation: String,
    )

    private val fsrs = FsrsEngine()

    fun refresh(app: WordFlowApp) {
        val wordMap = app.store.getWords()
        val reviews = app.store.getReviews()
        counts = fsrs.getQueueCounts(wordMap, reviews)

        val enriched = wordMap.values
            .sortedByDescending { it.createdAt }
            .map { e ->
                val card = reviews[e.id]
                val pr = app.store.parseResult(e)
                WordItem(
                    id = e.id,
                    word = e.word,
                    state = card?.state ?: 0,
                    phonetic = pr?.phonetic ?: "",
                    translation = pr?.translation ?: "",
                )
            }
        words = enriched
        filter()
    }

    fun onSearch(q: String) {
        search = q
        filter()
    }

    private fun filter() {
        filtered = if (search.isBlank()) words
        else words.filter { it.word.contains(search, ignoreCase = true) }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WordListScreen(onBack: () -> Unit, onWordClick: (String) -> Unit) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val vm: WordListViewModel = viewModel()

    LaunchedEffect(Unit) { vm.refresh(app) }

    Scaffold(
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0.dp, 0.dp, 0.dp, 0.dp),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            TopAppBar(
                title = { Text("词库") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回") }
                },
            )
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            // Legend
            Row(
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                LegendItem(STATE_NEW, "新词 ${vm.counts.new}")
                LegendItem(STATE_LEARNING, "学习中 ${vm.counts.learning}")
                LegendItem(STATE_REVIEW, "复习 ${vm.counts.review}")
                LegendItem(STATE_RELEARNING, "重学 ${vm.counts.relearning}")
            }

            OutlinedTextField(
                value = vm.search,
                onValueChange = { vm.onSearch(it) },
                label = { Text("搜索单词…") },
                leadingIcon = { Icon(Icons.Default.Search, contentDescription = null) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
            )

            LazyColumn(modifier = Modifier.fillMaxSize()) {
                items(vm.filtered) { item ->
                    WordRow(item, onWordClick)
                    HorizontalDivider(modifier = Modifier.padding(horizontal = 16.dp))
                }
                if (vm.filtered.isEmpty()) {
                    item {
                        EmptyState(
                            icon = Icons.Default.Search,
                            title = if (vm.words.isEmpty()) "暂无单词" else "未找到匹配的单词",
                            subtitle = if (vm.words.isEmpty()) "请在设置中同步单词" else "试试其他关键词",
                            modifier = Modifier.padding(top = 48.dp),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun WordRow(item: WordListViewModel.WordItem, onWordClick: (String) -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { onWordClick(item.id) }
            .padding(horizontal = 16.dp, vertical = 12.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StateDot(item.state)
                Text(item.word, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Medium)
            }
            if (item.state != STATE_NEW) StateBadge(item.state)
        }
        val line = buildString {
            if (item.phonetic.isNotBlank()) append(formatPhonetic(item.phonetic))
            if (item.translation.isNotBlank()) {
                if (isNotEmpty()) append(" · ")
                append(item.translation.replace("\n", " "))
            }
        }
        if (line.isNotBlank()) {
            Spacer(Modifier.height(2.dp))
            Text(
                line,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
private fun LegendItem(state: Int, label: String) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        StateDot(state)
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
