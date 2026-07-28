package com.wordflow.android.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/** Friendly labels for raw ECDICT exam-tag codes (fixes "zk gk" shown verbatim). */
private val EXAM_MAP = mapOf(
    "zk" to "中考", "gk" to "高考", "ky" to "考研",
    "cet4" to "CET-4", "cet6" to "CET-6",
    "toefl" to "TOEFL", "ielts" to "IELTS", "gre" to "GRE",
)

fun formatExamTag(tag: String): String =
    tag.split(Regex("\\s+"))
        .filter { it.isNotBlank() }
        .joinToString(" ") { code -> EXAM_MAP[code.lowercase()] ?: code.uppercase() }

@Composable
fun TagBadge(text: String, modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.tertiaryContainer,
        contentColor = MaterialTheme.colorScheme.onTertiaryContainer,
        shape = MaterialTheme.shapes.small,
        modifier = modifier,
    ) {
        Text(text, modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
fun CollinsBadge(stars: Int, modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.primaryContainer,
        contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
        shape = MaterialTheme.shapes.small,
        modifier = modifier,
    ) {
        Text("★ $stars", modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
fun OxfordBadge(modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.secondaryContainer,
        contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
        shape = MaterialTheme.shapes.small,
        modifier = modifier,
    ) {
        Text("Oxford", modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
fun MetaBadges(tag: String, collins: Int?, oxford: Int?, modifier: Modifier = Modifier) {
    val hasTag = tag.isNotBlank()
    val hasCollins = collins != null && collins > 0
    val hasOxford = oxford != null && oxford > 0
    if (!hasTag && !hasCollins && !hasOxford) return
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        if (hasTag) TagBadge(formatExamTag(tag))
        if (hasCollins) CollinsBadge(collins!!)
        if (hasOxford) OxfordBadge()
    }
}
