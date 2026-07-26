package com.wordflow.android.ui.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.data.STATE_LABELS

class WordListViewModel : ViewModel() {
    var words by mutableStateOf<List<WordItem>>(emptyList())
    var search by mutableStateOf("")
    var filtered by mutableStateOf<List<WordItem>>(emptyList())
    var counts by mutableStateOf(FsrsEngine.QueueCounts(0, 0, 0, 0, 0))

    data class WordItem(
        val id: String,
        val word: String,
        val state: Int,
        val stateLabel: String
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
                val state = card?.state ?: 0
                WordItem(e.id, e.word, state, STATE_LABELS[state] ?: "New")
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
        topBar = {
            TopAppBar(
                title = { Text("Word List") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Back") }
                }
            )
        }
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            // Stats row
            Row(modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                BadgeText("New ${vm.counts.new}")
                BadgeText("LRN ${vm.counts.learning}")
                BadgeText("REV ${vm.counts.review}")
            }

            // Search
            OutlinedTextField(
                value = vm.search,
                onValueChange = { vm.onSearch(it) },
                label = { Text("Search words…") },
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                singleLine = true,
                leadingIcon = { Icon(Icons.Default.Search, null) }
            )

            // Word list
            LazyColumn(modifier = Modifier.fillMaxSize()) {
                items(vm.filtered) { item ->
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onWordClick(item.id) }
                            .padding(horizontal = 16.dp, vertical = 12.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(item.word, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
                        Text(item.stateLabel, style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Divider(modifier = Modifier.padding(horizontal = 16.dp))
                }

                if (vm.filtered.isEmpty()) {
                    item {
                        Text(
                            if (vm.words.isEmpty()) "No words yet. Pull from server first."
                            else "No matches for \"${vm.search}\"",
                            modifier = Modifier.padding(32.dp),
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun BadgeText(text: String) {
    Surface(color = MaterialTheme.colorScheme.secondaryContainer, shape = MaterialTheme.shapes.small) {
        Text(text, modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
            style = MaterialTheme.typography.labelSmall)
    }
}
