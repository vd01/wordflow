package com.wordflow.android

import android.app.Application
import com.wordflow.android.data.Store
import com.wordflow.android.data.SyncClient

class WordFlowApp : Application() {
    lateinit var store: Store
    lateinit var syncClient: SyncClient
        private set
    lateinit var themeState: com.wordflow.android.ui.theme.ThemeState
        private set

    override fun onCreate() {
        super.onCreate()
        store = Store(this)
        syncClient = SyncClient(this)
        themeState = com.wordflow.android.ui.theme.ThemeState.create(this)
    }
}
