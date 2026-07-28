package com.wordflow.android.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.wordflow.android.data.STATE_LEARNING
import com.wordflow.android.data.STATE_NEW
import com.wordflow.android.data.STATE_RELEARNING
import com.wordflow.android.data.STATE_REVIEW
import com.wordflow.android.ui.theme.StateLearningColor
import com.wordflow.android.ui.theme.StateNewColor
import com.wordflow.android.ui.theme.StateRelearningColor
import com.wordflow.android.ui.theme.StateReviewColor

data class StateStyle(val color: Color, val label: String)

fun stateStyle(state: Int): StateStyle = when (state) {
    STATE_NEW -> StateStyle(StateNewColor, "新词")
    STATE_LEARNING -> StateStyle(StateLearningColor, "学习中")
    STATE_REVIEW -> StateStyle(StateReviewColor, "复习")
    STATE_RELEARNING -> StateStyle(StateRelearningColor, "重学")
    else -> StateStyle(StateNewColor, "新词")
}

@Composable
fun StateDot(state: Int, modifier: Modifier = Modifier) {
    val style = stateStyle(state)
    Box(
        modifier
            .size(8.dp)
            .clip(CircleShape)
            .background(style.color)
    )
}

@Composable
fun StateBadge(state: Int, modifier: Modifier = Modifier) {
    val style = stateStyle(state)
    Surface(
        color = style.color.copy(alpha = 0.16f),
        contentColor = style.color,
        shape = MaterialTheme.shapes.small,
        modifier = modifier,
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
            verticalAlignment = androidx.compose.ui.Alignment.CenterVertically,
        ) {
            Box(
                Modifier
                    .size(8.dp)
                    .clip(CircleShape)
                    .background(style.color)
            )
            Text(style.label, style = MaterialTheme.typography.labelSmall)
        }
    }
}
