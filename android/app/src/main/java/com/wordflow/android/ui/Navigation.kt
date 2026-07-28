package com.wordflow.android.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.School
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
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

private data class Tab(val route: String, val label: String, val icon: ImageVector)

private val TABS = listOf(
    Tab("home", "首页", Icons.Default.Home),
    Tab("review", "复习", Icons.Default.School),
    Tab("library", "词库", Icons.AutoMirrored.Filled.List),
    Tab("settings", "设置", Icons.Default.Settings),
)

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

        val backStackEntry by navController.currentBackStackEntryAsState()
        val currentRoute = backStackEntry?.destination?.route
        val showBar = currentRoute != null && currentRoute in TABS.map { it.route }

        fun navigateTab(route: String) {
            navController.navigate(route) {
                popUpTo(navController.graph.startDestinationId) { saveState = true }
                launchSingleTop = true
                restoreState = true
            }
        }

        Scaffold(
            contentWindowInsets = WindowInsets(0.dp, 0.dp, 0.dp, 0.dp),
            bottomBar = {
                if (showBar) {
                    NavigationBar {
                        TABS.forEach { tab ->
                            NavigationBarItem(
                                selected = currentRoute == tab.route,
                                onClick = { navigateTab(tab.route) },
                                icon = { Icon(tab.icon, contentDescription = tab.label) },
                                label = { Text(tab.label) },
                            )
                        }
                    }
                }
            },
        ) { padding ->
            NavHost(
                navController = navController,
                startDestination = startDest,
                modifier = Modifier.padding(padding),
            ) {
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
                        onNavigateToReview = { navigateTab("review") },
                        onNavigateToSettings = { navigateTab("settings") },
                    )
                }
                composable("review") {
                    ReviewScreen(
                        onBack = { navController.popBackStack() },
                        onGoSettings = { navigateTab("settings") },
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
                        onBack = { navigateTab("home") },
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
}
