package com.wordflow.android

import android.app.Application
import com.wordflow.android.data.Store

class WordFlowApp : Application() {
    lateinit var store: Store
    lateinit var themeState: com.wordflow.android.ui.theme.ThemeState
        private set

    override fun onCreate() {
        super.onCreate()
        store = Store(this)
        themeState = com.wordflow.android.ui.theme.ThemeState.create(this)
    }
}
