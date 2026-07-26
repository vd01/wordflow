package com.wordflow.android.ui.screen

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.FsrsEngine
import com.wordflow.android.data.SyncClient
import kotlinx.coroutines.launch

class HomeViewModel : ViewModel() {
    var wordCount by mutableStateOf(0)
    var dueCount by mutableStateOf(0)
    var lastSyncDisplay by mutableStateOf("never")
    var dailyLimit by mutableStateOf(0)
    var dailyNewCount by mutableStateOf(0)
    var status by mutableStateOf("")
    var busy by mutableStateOf(false)
    var email by mutableStateOf("")

    private val client = SyncClient()
    private val fsrs = FsrsEngine()

    fun refresh(app: WordFlowApp) {
        val store = app.store
        val words = store.getWords()
        val reviews = store.getReviews()
        val counts = fsrs.getQueueCounts(words, reviews)
        val dailyCount = store.getDailyCount()

        wordCount = words.size
        dueCount = counts.total
        lastSyncDisplay = formatSyncTime(store.lastSync)
        dailyLimit = store.dailyLimit
        dailyNewCount = dailyCount.newCount
        email = store.userEmail
    }

    fun sync(app: WordFlowApp, since: Long? = null) {
        val store = app.store
        if (!store.isLoggedIn) return
        busy = true
        status = "Syncing..."
        viewModelScope.launch {
            try {
                val effectiveSince = since ?: store.lastSync
                val res = client.pull(store.serverAddr, store.token, effectiveSince)
                val r = store.mergePulled(res.entries, res.serverNow, effectiveSince == 0L)
                refresh(app)
                status = "Synced ${r.changed} entries, local total: ${r.total}"
            } catch (e: Exception) {
                status = "Sync failed: ${e.message}"
            } finally {
                busy = false
            }
        }
    }

    fun testConnection(app: WordFlowApp) {
        val store = app.store
        busy = true
        status = "Testing..."
        viewModelScope.launch {
            try {
                val h = client.health(store.serverAddr)
                var s = "Connected: ${h.service} v${h.version}"
                if (h.email) s += " (Email auth enabled)"
                if (store.isLoggedIn) {
                    try {
                        val st = client.getStatus(store.serverAddr, store.token)
                        s += " | Remote words: ${st.wordCount}"
                    } catch (_: Exception) {
                        s += " | Token invalid"
                    }
                }
                status = s
            } catch (e: Exception) {
                status = "Connection failed: ${e.message}"
            } finally {
                busy = false
            }
        }
    }

    fun setDailyLimit(app: WordFlowApp, n: Int) {
        app.store.dailyLimit = n
        dailyLimit = n
    }

    fun logout(app: WordFlowApp) {
        app.store.token = ""
        app.store.userEmail = ""
        app.store.lastSync = 0
    }

    private fun formatSyncTime(ts: Long): String {
        if (ts <= 0) return "never"
        val now = System.currentTimeMillis() / 1000
        val diff = now - ts
        return when {
            diff < 60 -> "just now"
            diff < 3600 -> "${diff / 60} min ago"
            diff < 86400 -> "${diff / 3600} hr ago"
            diff < 604800 -> "${diff / 86400} days ago"
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
    onNavigateToWordList: () -> Unit,
    onLogout: () -> Unit
) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val vm: HomeViewModel = viewModel()

    LaunchedEffect(Unit) {
        vm.refresh(app)
        // Auto-sync on first load
        if (app.store.isLoggedIn) {
            vm.sync(app)
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("查词温故 WordFlow") },
                actions = {
                    IconButton(onClick = { vm.logout(app); onLogout() }) {
                        Icon(Icons.Default.Logout, contentDescription = "Logout")
                    }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            // User info
            Card(modifier = Modifier.fillMaxWidth()) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Default.CheckCircle, contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary)
                        Spacer(Modifier.width(8.dp))
                        Text("Synced", style = MaterialTheme.typography.titleMedium)
                    }
                    Spacer(Modifier.height(4.dp))
                    Text("Email: ${vm.email}", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(Modifier.height(8.dp))
                    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { vm.testConnection(app) }, enabled = !vm.busy) {
                            Text("Test")
                        }
                        OutlinedButton(onClick = { vm.sync(app, 0) }, enabled = !vm.busy) {
                            Text("Pull All")
                        }
                    }
                }
            }

            // Stats
            Card(modifier = Modifier.fillMaxWidth()) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text("Local words: ${vm.wordCount}")
                    Text("Due for review: ${vm.dueCount}")
                    Text("Last sync: ${vm.lastSyncDisplay}",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodySmall)
                }
            }

            // Daily limit
            Card(modifier = Modifier.fillMaxWidth()) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text("Daily new word limit", style = MaterialTheme.typography.titleSmall)
                    Spacer(Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        listOf(0, 5, 10, 20, 50).forEach { n ->
                            FilterChip(
                                selected = vm.dailyLimit == n,
                                onClick = { vm.setDailyLimit(app, n) },
                                label = { Text(if (n == 0) "∞" else "$n") }
                            )
                        }
                    }
                    Text("Today's new words: ${vm.dailyNewCount}${if (vm.dailyLimit > 0) " / ${vm.dailyLimit}" else ""}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }

            // Status
            if (vm.status.isNotBlank()) {
                Text(vm.status, style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }

            Spacer(Modifier.weight(1f))

            // Action buttons
            OutlinedButton(
                onClick = onNavigateToWordList,
                modifier = Modifier.fillMaxWidth()
            ) {
                Icon(Icons.Default.List, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Word List")
            }

            Button(
                onClick = onNavigateToReview,
                modifier = Modifier.fillMaxWidth(),
                enabled = vm.dueCount > 0
            ) {
                Icon(Icons.Default.School, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Start Review (${vm.dueCount})")
            }
        }
    }
}
