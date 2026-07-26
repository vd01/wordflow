package com.wordflow.android.ui.screen

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Email
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wordflow.android.WordFlowApp
import com.wordflow.android.data.SyncClient
import kotlinx.coroutines.launch

class LoginViewModel : ViewModel() {
    var email by mutableStateOf("")
    var code by mutableStateOf("")
    var status by mutableStateOf("")
    var busy by mutableStateOf(false)
    var codeSent by mutableStateOf(false)
    var loginSuccess by mutableStateOf(false)

    private val client = SyncClient()

    fun sendCode(app: WordFlowApp) {
        if (email.isBlank()) {
            status = "Please enter your email"
            return
        }
        busy = true
        status = "Sending code..."
        viewModelScope.launch {
            try {
                val msg = client.requestEmailCode(app.store.serverAddr, email.trim())
                codeSent = true
                status = msg.ifBlank { "Verification code sent to your email" }
            } catch (e: Exception) {
                status = "Failed: ${e.message}"
            } finally {
                busy = false
            }
        }
    }

    fun verifyCode(app: WordFlowApp) {
        if (code.isBlank()) {
            status = "Please enter the verification code"
            return
        }
        busy = true
        status = "Verifying..."
        viewModelScope.launch {
            try {
                val result = client.verifyEmailCode(app.store.serverAddr, email.trim(), code.trim())
                if (result.token.isNotBlank()) {
                    app.store.token = result.token
                    app.store.userEmail = email.trim()
                    loginSuccess = true
                    status = "Login successful!"
                } else {
                    status = result.message.ifBlank { "Verification failed" }
                }
            } catch (e: Exception) {
                status = "Failed: ${e.message}"
            } finally {
                busy = false
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LoginScreen(onLoginSuccess: () -> Unit) {
    val app = androidx.compose.ui.platform.LocalContext.current.applicationContext as WordFlowApp
    val vm: LoginViewModel = viewModel()
    val snackbarHostState = remember { SnackbarHostState() }

    // Navigate on success
    LaunchedEffect(vm.loginSuccess) {
        if (vm.loginSuccess) onLoginSuccess()
    }

    Scaffold(snackbarHost = { SnackbarHost(snackbarHostState) }) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(32.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center
        ) {
            // Logo / Title
            Text(
                "查词温故",
                style = MaterialTheme.typography.headlineLarge,
                color = MaterialTheme.colorScheme.primary
            )
            Text(
                "WordFlow",
                style = MaterialTheme.typography.headlineMedium,
                color = MaterialTheme.colorScheme.secondary
            )
            Spacer(Modifier.height(8.dp))
            Text(
                "Sign in with email to sync your words",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )

            Spacer(Modifier.height(32.dp))

            // Email input
            OutlinedTextField(
                value = vm.email,
                onValueChange = { vm.email = it; vm.codeSent = false },
                label = { Text("Email") },
                leadingIcon = { Icon(Icons.Default.Email, contentDescription = null) },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email),
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                enabled = !vm.busy && !vm.codeSent
            )

            Spacer(Modifier.height(16.dp))

            // Send code button
            Button(
                onClick = { vm.sendCode(app) },
                enabled = !vm.busy && vm.email.isNotBlank() && !vm.codeSent,
                modifier = Modifier.fillMaxWidth()
            ) {
                if (vm.busy && !vm.codeSent) {
                    CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(8.dp))
                }
                Text(if (vm.codeSent) "Code Sent ✓" else "Send Verification Code")
            }

            // Code input (shown after code is sent)
            if (vm.codeSent) {
                Spacer(Modifier.height(24.dp))
                OutlinedTextField(
                    value = vm.code,
                    onValueChange = { vm.code = it },
                    label = { Text("Verification Code") },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    enabled = !vm.busy
                )

                Spacer(Modifier.height(16.dp))

                Button(
                    onClick = { vm.verifyCode(app) },
                    enabled = !vm.busy && vm.code.isNotBlank(),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    if (vm.busy) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(20.dp),
                            strokeWidth = 2.dp,
                            color = MaterialTheme.colorScheme.onPrimary
                        )
                        Spacer(Modifier.width(8.dp))
                    }
                    Text("Sign In")
                }
            }

            // Status message
            if (vm.status.isNotBlank()) {
                Spacer(Modifier.height(16.dp))
                Text(
                    vm.status,
                    style = MaterialTheme.typography.bodySmall,
                    color = if (vm.status.startsWith("Failed") || vm.status.startsWith("Please"))
                        MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}
