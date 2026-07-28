package com.wordflow.android.ui.components

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

@Composable
fun SectionLabel(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.titleSmall,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.primary,
    )
}

@Composable
fun SectionBlock(
    label: String,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(modifier.fillMaxWidth().padding(vertical = 6.dp)) {
        SectionLabel(label)
        Spacer(Modifier.height(4.dp))
        Column(content = content)
    }
}

@Composable
fun SectionText(text: String) {
    Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurface)
}

@Composable
fun ExampleBlock(english: String, chinese: String, modifier: Modifier = Modifier) {
    Surface(
        modifier = modifier.fillMaxWidth(),
        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.45f),
        shape = MaterialTheme.shapes.medium,
    ) {
        Column(Modifier.padding(12.dp)) {
            Text("“$english”", style = MaterialTheme.typography.bodyMedium, fontStyle = FontStyle.Italic)
            if (chinese.isNotBlank()) {
                Spacer(Modifier.height(4.dp))
                Text(
                    chinese,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
fun DefinitionBlock(
    pos: String,
    meaning: String,
    englishExample: String,
    chineseExample: String,
    modifier: Modifier = Modifier,
) {
    Column(modifier.fillMaxWidth().padding(vertical = 4.dp)) {
        if (pos.isNotBlank()) {
            Text(
                pos,
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.tertiary,
            )
        }
        if (meaning.isNotBlank()) {
            Text(meaning, style = MaterialTheme.typography.bodyMedium)
        }
        if (englishExample.isNotBlank() || chineseExample.isNotBlank()) {
            Spacer(Modifier.height(6.dp))
            ExampleBlock(english = englishExample, chinese = chineseExample)
        }
    }
}
