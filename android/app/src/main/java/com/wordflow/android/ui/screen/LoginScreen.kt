package com.wordflow.android.ui.screen

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Email
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.MenuBook
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.SyncClient
import com.wordflow.android.ui.components.StatusBanner
import com.wordflow.android.ui.components.StatusKind
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

enum class LoginMode { EMAIL, PAIR }

class LoginViewModel : ViewModel() {
    var email by mutableStateOf("")
    var code by mutableStateOf("")
    var pairCode by mutableStateOf("")
    var status by mutableStateOf("")
    var statusKind by mutableStateOf(StatusKind.INFO)
    var busy by mutableStateOf(false)
    var codeSent by mutableStateOf(false)
    var loginSuccess by mutableStateOf(false)
    var loginMode by mutableStateOf(LoginMode.EMAIL)

    private val client = SyncClient()

    private fun info(msg: String) { status = msg; statusKind = StatusKind.INFO }
    private fun success(msg: String) { status = msg; statusKind = StatusKind.SUCCESS }
    private fun error(msg: String) { status = msg; statusKind = StatusKind.ERROR }
    private fun loading(msg: String) { status = msg; statusKind = StatusKind.LOADING }

    fun sendCode(app: WordFlowApp) {
        if (email.isBlank()) { error("请输入邮箱"); return }
        busy = true; loading("正在发送验证码…")
        viewModelScope.launch {
            try {
                val msg = withContext(Dispatchers.IO) { client.requestEmailCode(app.store.serverAddr, email.trim()) }
                codeSent = true
                success(msg.ifBlank { "验证码已发送至邮箱" })
            } catch (e: Exception) {
                error("发送失败：${e.message ?: e.javaClass.simpleName}")
            } finally { busy = false }
        }
    }

    fun verifyCode(app: WordFlowApp) {
        if (code.isBlank()) { error("请输入验证码"); return }
        busy = true; loading("正在验证…")
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { client.verifyEmailCode(app.store.serverAddr, email.trim(), code.trim()) }
                if (result.token.isNotBlank()) {
                    app.store.token = result.token
                    app.store.userEmail = email.trim()
                    loginSuccess = true
                    success("登录成功")
                } else error(result.message.ifBlank { "验证失败" })
            } catch (e: Exception) {
                error("验证失败：${e.message ?: e.javaClass.simpleName}")
            } finally { busy = false }
        }
    }

    fun verifyPairCode(app: WordFlowApp) {
        if (pairCode.isBlank()) { error("请输入配对码"); return }
        busy = true; loading("正在验证…")
        viewModelScope.launch {
            try {
                val result = withContext(Dispatchers.IO) { client.verifyPairCode(app.store.serverAddr, pairCode.trim()) }
                if (result.token.isNotBlank()) {
                    app.store.token = result.token
                    loginSuccess = true
                    success("登录成功")
                } else error(result.message.ifBlank { "配对码无效" })
            } catch (e: Exception) {
                error("验证失败：${e.message ?: e.javaClass.simpleName}")
            } finally { busy = false }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LoginScreen(onLoginSuccess: () -> Unit) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val vm: LoginViewModel = viewModel()

    LaunchedEffect(vm.loginSuccess) { if (vm.loginSuccess) onLoginSuccess() }

    Scaffold(containerColor = MaterialTheme.colorScheme.background) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .imePadding(),
            contentAlignment = Alignment.Center,
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 24.dp, vertical = 32.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(Dimens_sm),
            ) {
                // Logo mark
                Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = CircleShape, modifier = Modifier.size(72.dp)) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(Icons.Default.MenuBook, contentDescription = null, modifier = Modifier.size(40.dp), tint = MaterialTheme.colorScheme.primary)
                    }
                }
                Spacer(Modifier.height(12.dp))
                Text("查词温故", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary)
                Text("WordFlow", style = MaterialTheme.typography.titleLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text("记住每一个单词", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)

                Spacer(Modifier.height(16.dp))
                TabRow(selectedTabIndex = if (vm.loginMode == LoginMode.EMAIL) 0 else 1) {
                    Tab(
                        selected = vm.loginMode == LoginMode.EMAIL,
                        onClick = { vm.loginMode = LoginMode.EMAIL; vm.status = "" },
                        text = { Text("邮箱") },
                        icon = { Icon(Icons.Default.Email, contentDescription = null, modifier = Modifier.size(20.dp)) },
                    )
                    Tab(
                        selected = vm.loginMode == LoginMode.PAIR,
                        onClick = { vm.loginMode = LoginMode.PAIR; vm.status = "" },
                        text = { Text("配对码") },
                        icon = { Icon(Icons.Default.Link, contentDescription = null, modifier = Modifier.size(20.dp)) },
                    )
                }

                Spacer(Modifier.height(16.dp))
                when (vm.loginMode) {
                    LoginMode.EMAIL -> EmailLoginSection(vm, app)
                    LoginMode.PAIR -> PairCodeLoginSection(vm, app)
                }

                if (vm.status.isNotBlank()) {
                    Spacer(Modifier.height(12.dp))
                    StatusBanner(text = vm.status, kind = vm.statusKind)
                }
                Spacer(Modifier.height(48.dp))
            }
        }
    }
}

@Composable
private fun EmailLoginSection(vm: LoginViewModel, app: WordFlowApp) {
    Text("用邮箱登录以同步你的单词", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    Spacer(Modifier.height(16.dp))
    OutlinedTextField(
        value = vm.email,
        onValueChange = { vm.email = it; vm.codeSent = false },
        label = { Text("邮箱") },
        leadingIcon = { Icon(Icons.Default.Email, contentDescription = null) },
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email),
        singleLine = true,
        modifier = Modifier.fillMaxWidth(),
        enabled = !vm.busy && !vm.codeSent,
    )
    Spacer(Modifier.height(12.dp))
    Button(
        onClick = { vm.sendCode(app) },
        enabled = !vm.busy && vm.email.isNotBlank() && !vm.codeSent,
        modifier = Modifier.fillMaxWidth(),
        shape = MaterialTheme.shapes.medium,
    ) {
        if (vm.busy && !vm.codeSent) { CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = MaterialTheme.colorScheme.onPrimary); Spacer(Modifier.width(Dimens_sm)) }
        Text(if (vm.codeSent) "验证码已发送" else "发送验证码")
    }
    if (vm.codeSent) {
        Spacer(Modifier.height(16.dp))
        OutlinedTextField(
            value = vm.code,
            onValueChange = { vm.code = it },
            label = { Text("验证码") },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
            enabled = !vm.busy,
        )
        Spacer(Modifier.height(12.dp))
        Button(
            onClick = { vm.verifyCode(app) },
            enabled = !vm.busy && vm.code.isNotBlank(),
            modifier = Modifier.fillMaxWidth(),
            shape = MaterialTheme.shapes.medium,
        ) {
            if (vm.busy) { CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = MaterialTheme.colorScheme.onPrimary); Spacer(Modifier.width(Dimens_sm)) }
            Text("登录")
        }
    }
}

@Composable
private fun PairCodeLoginSection(vm: LoginViewModel, app: WordFlowApp) {
    Text("在电脑端生成配对码后在此输入", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    Spacer(Modifier.height(4.dp))
    Text("电脑端：设置 → 同步 → 生成配对码", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    Spacer(Modifier.height(16.dp))
    OutlinedTextField(
        value = vm.pairCode,
        onValueChange = { vm.pairCode = it },
        label = { Text("配对码") },
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
        singleLine = true,
        modifier = Modifier.fillMaxWidth(),
        enabled = !vm.busy,
    )
    Spacer(Modifier.height(12.dp))
    Button(
        onClick = { vm.verifyPairCode(app) },
        enabled = !vm.busy && vm.pairCode.isNotBlank(),
        modifier = Modifier.fillMaxWidth(),
        shape = MaterialTheme.shapes.medium,
    ) {
        if (vm.busy) { CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = MaterialTheme.colorScheme.onPrimary); Spacer(Modifier.width(Dimens_sm)) }
        Text("关联设备")
    }
}

private val Dimens_sm = 8.dp

