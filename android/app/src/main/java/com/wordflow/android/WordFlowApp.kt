package com.wordflow.android

import android.app.Application
import com.wordflow.android.data.Store

class WordFlowApp : Application() {
    lateinit var store: Store
        private set

    override fun onCreate() {
        super.onCreate()
        store = Store(this)
    }
}
