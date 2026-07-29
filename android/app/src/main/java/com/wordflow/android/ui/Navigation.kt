package com.wordflow.android.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.wordflow.android.WordFlowApp
import com.wordflow.android.ui.screen.HomeScreen
import com.wordflow.android.ui.screen.LoginScreen
import com.wordflow.android.ui.screen.ReviewScreen
import com.wordflow.android.ui.screen.SettingsScreen
import com.wordflow.android.ui.screen.WordDetailScreen
import com.wordflow.android.ui.screen.WordListScreen
import com.wordflow.android.ui.theme.ThemeState
import com.wordflow.android.ui.theme.WordFlowTheme

@Composable
fun WordFlowApp() {
    val app = LocalContext.current.applicationContext as WordFlowApp
    val themeState = app.themeState
    val darkTheme = when (themeState.darkMode) {
        ThemeState.MODE_LIGHT -> false
        ThemeState.MODE_DARK -> true
        else -> isSystemInDarkTheme()
    }
    WordFlowTheme(darkTheme = darkTheme, dynamicColor = themeState.dynamicColor) {
        val navController = rememberNavController()
        val startDest = if (app.store.isLoggedIn) "home" else "login"

        NavHost(navController = navController, startDestination = startDest) {
            composable("login") {
                LoginScreen(
                    onLoginSuccess = {
                        navController.navigate("home") {
                            popUpTo("login") { inclusive = true }
                        }
                    },
                )
            }
            composable("home") {
                HomeScreen(
                    onNavigateToReview = { navController.navigate("review") },
                    onNavigateToWordList = { navController.navigate("library") },
                    onNavigateToSettings = { navController.navigate("settings") },
                )
            }
            composable("review") {
                ReviewScreen(
                    onBack = { navController.popBackStack() },
                    onGoSettings = { navController.navigate("settings") },
                )
            }
            composable("library") {
                WordListScreen(
                    onBack = { navController.popBackStack() },
                    onWordClick = { id -> navController.navigate("worddetail/$id") },
                )
            }
            composable("settings") {
                SettingsScreen(
                    onBack = { navController.popBackStack() },
                    onLogout = {
                        navController.navigate("login") {
                            popUpTo(navController.graph.startDestinationId) { inclusive = true }
                        }
                    },
                )
            }
            composable(
                route = "worddetail/{wordId}",
                arguments = listOf(navArgument("wordId") { type = NavType.StringType }),
            ) { entry ->
                val wordId = entry.arguments?.getString("wordId") ?: ""
                WordDetailScreen(wordId = wordId, onBack = { navController.popBackStack() })
            }
        }
    }
}
