package com.wordflow.android.ui.screen

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CloudSync
import androidx.compose.material.icons.filled.Logout
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.SyncClient
import com.wordflow.android.ui.components.StatusBanner
import com.wordflow.android.ui.components.StatusKind
import com.wordflow.android.ui.theme.Dimens
import com.wordflow.android.ui.theme.ThemeState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class Status(val text: String, val kind: StatusKind)

class SettingsViewModel : ViewModel() {
    var serverAddr by mutableStateOf("")
    var userEmail by mutableStateOf("")
    var isPaired by mutableStateOf(false)
    var lastSyncDisplay by mutableStateOf("从未同步")
    var dailyLimit by mutableStateOf(0)
    var dailyNewCount by mutableStateOf(0)
    var status by mutableStateOf<Status?>(null)
    var busy by mutableStateOf(false)

    private val client = SyncClient()

    fun refresh(app: WordFlowApp) {
        val store = app.store
        serverAddr = store.serverAddr
        userEmail = store.userEmail
        isPaired = store.userEmail.isBlank() && store.isLoggedIn
        lastSyncDisplay = formatSyncTime(store.lastSync)
        dailyLimit = store.dailyLimit
        dailyNewCount = store.getDailyCount().newCount
    }

    fun sync(app: WordFlowApp) {
        val store = app.store
        if (!store.isLoggedIn) return
        busy = true
        status = Status("正在同步…", StatusKind.LOADING)
        viewModelScope.launch {
            try {
                val res = withContext(Dispatchers.IO) { client.pull(store.serverAddr, store.token, store.lastSync) }
                val r = store.mergePulled(res.entries, res.serverNow, store.lastSync == 0L)
                refresh(app)
                status = Status("已同步 ${r.changed} 条", StatusKind.SUCCESS)
            } catch (e: Exception) {
                status = Status("同步失败：${e.message ?: e.javaClass.simpleName}", StatusKind.ERROR)
            } finally {
                busy = false
            }
        }
    }

    fun testConnection(app: WordFlowApp) {
        val store = app.store
        busy = true
        status = Status("正在测试…", StatusKind.LOADING)
        viewModelScope.launch {
            try {
                val h = withContext(Dispatchers.IO) { client.health(store.serverAddr) }
                var s = "已连接：${h.service} v${h.version}"
                if (h.email) s += "（邮箱登录）"
                status = Status(s, StatusKind.SUCCESS)
            } catch (e: Exception) {
                status = Status("连接失败：${e.message}", StatusKind.ERROR)
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
        val store = app.store
        store.token = ""
        store.userEmail = ""
        store.lastSync = 0
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
fun SettingsScreen(onBack: () -> Unit, onLogout: () -> Unit) {
    val app = LocalContext.current.applicationContext as WordFlowApp
    val vm: SettingsViewModel = viewModel()
    val themeState = app.themeState

    LaunchedEffect(Unit) { vm.refresh(app) }

    Scaffold(
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0.dp, 0.dp, 0.dp, 0.dp),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            TopAppBar(
                title = { Text("设置") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(Dimens.screenPadding),
            verticalArrangement = Arrangement.spacedBy(Dimens.md),
        ) {
            SettingsCard("账户") {
                SettingRow("登录方式", if (vm.isPaired) "配对码登录" else vm.userEmail.ifBlank { "未登录" })
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    OutlinedButton(onClick = { vm.logout(app); onLogout() }) {
                        Icon(Icons.Default.Logout, contentDescription = null)
                        Spacer(Modifier.width(8.dp))
                        Text("退出登录")
                    }
                }
            }

            SettingsCard("同步") {
                SettingRow("服务器地址", vm.serverAddr)
                SettingRow("上次同步", vm.lastSyncDisplay)
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(
                        onClick = { vm.testConnection(app) },
                        enabled = !vm.busy,
                        modifier = Modifier.weight(1f),
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                    ) {
                        Icon(Icons.Default.CloudSync, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("测试连接", maxLines = 1)
                    }
                    Button(
                        onClick = { vm.sync(app) },
                        enabled = !vm.busy,
                        modifier = Modifier.weight(1f),
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                    ) {
                        Icon(Icons.Default.Refresh, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("立即同步", maxLines = 1)
                    }
                }
            }

            SettingsCard("学习") {
                Text("每日新词上限", style = MaterialTheme.typography.bodyMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    listOf(0, 5, 10, 20, 50).forEach { n ->
                        OptionChip(
                            text = if (n == 0) "不限" else "$n",
                            selected = vm.dailyLimit == n,
                            onClick = { vm.setDailyLimit(app, n) },
                        )
                    }
                }
                Text(
                    "今日已学新词：${vm.dailyNewCount}${if (vm.dailyLimit > 0) " / ${vm.dailyLimit}" else ""}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            SettingsCard("外观") {
                Text("主题", style = MaterialTheme.typography.bodyMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    listOf(
                        ThemeState.MODE_SYSTEM to "跟随系统",
                        ThemeState.MODE_LIGHT to "浅色",
                        ThemeState.MODE_DARK to "深色",
                    ).forEach { (mode, label) ->
                        OptionChip(
                            text = label,
                            selected = themeState.darkMode == mode,
                            onClick = { themeState.updateDarkMode(mode) },
                        )
                    }
                }
                Spacer(Modifier.height(4.dp))
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Column {
                        Text("动态取色（Material You）", style = MaterialTheme.typography.bodyMedium)
                        Text(
                            "根据壁纸自动配色，需 Android 12+",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Switch(
                        checked = themeState.dynamicColor,
                        onCheckedChange = { themeState.updateDynamicColor(it) },
                    )
                }
            }

            SettingsCard("关于") {
                SettingRow("应用", "查词温故 WordFlow 1.0.0")
            }

            vm.status?.let { StatusBanner(text = it.text, kind = it.kind) }

            Spacer(Modifier.height(Dimens.lg))
        }
    }
}

@Composable
private fun SettingsCard(title: String, content: @Composable ColumnScope.() -> Unit) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = MaterialTheme.shapes.large,
        color = MaterialTheme.colorScheme.surface,
        tonalElevation = 1.dp,
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(Dimens.sm)) {
            Text(title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.primary)
            content()
        }
    }
}

@Composable
private fun SettingRow(label: String, value: String) {
    Column(Modifier.fillMaxWidth().padding(vertical = 2.dp)) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun OptionChip(text: String, selected: Boolean, onClick: () -> Unit) {
    val container = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface
    val onContainer = if (selected) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurface
    val borderColor = if (selected) Color.Transparent else MaterialTheme.colorScheme.outline
    Surface(
        color = container,
        contentColor = onContainer,
        shape = MaterialTheme.shapes.small,
        border = BorderStroke(1.dp, borderColor),
        modifier = Modifier.clickable(onClick = onClick),
    ) {
        Text(
            text,
            style = MaterialTheme.typography.labelLarge,
            maxLines = 1,
            modifier = Modifier.padding(horizontal = 14.dp, vertical = 8.dp),
        )
    }
}
