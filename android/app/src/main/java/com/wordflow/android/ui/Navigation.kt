package com.wordflow.android.ui

import androidx.compose.runtime.Composable
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.wordflow.android.ui.screen.*
import com.wordflow.android.ui.theme.WordFlowTheme

@Composable
fun WordFlowApp() {
    WordFlowTheme {
        val navController = rememberNavController()
        NavHost(navController = navController, startDestination = "login") {
            composable("login") {
                LoginScreen(
                    onLoginSuccess = {
                        navController.navigate("home") {
                            popUpTo("login") { inclusive = true }
                        }
                    }
                )
            }
            composable("home") {
                HomeScreen(
                    onNavigateToReview = { navController.navigate("review") },
                    onNavigateToWordList = { navController.navigate("wordlist") },
                    onLogout = {
                        navController.navigate("login") {
                            popUpTo("home") { inclusive = true }
                        }
                    }
                )
            }
            composable("review") {
                ReviewScreen(
                    onBack = { navController.popBackStack() },
                    onGoSettings = {
                        navController.popBackStack()
                    }
                )
            }
            composable("wordlist") {
                WordListScreen(
                    onBack = { navController.popBackStack() },
                    onWordClick = { id -> navController.navigate("worddetail/$id") }
                )
            }
            composable(
                route = "worddetail/{wordId}",
                arguments = listOf(navArgument("wordId") { type = NavType.StringType })
            ) { backStackEntry ->
                val wordId = backStackEntry.arguments?.getString("wordId") ?: ""
                WordDetailScreen(
                    wordId = wordId,
                    onBack = { navController.popBackStack() }
                )
            }
        }
    }
}
