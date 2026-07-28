package com.wordflow.android.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wordflow.android.data.RATING_AGAIN
import com.wordflow.android.data.RATING_EASY
import com.wordflow.android.data.RATING_GOOD
import com.wordflow.android.data.RATING_HARD
import com.wordflow.android.ui.theme.Dimens
import com.wordflow.android.ui.theme.RatingAgain
import com.wordflow.android.ui.theme.RatingEasy
import com.wordflow.android.ui.theme.RatingGood
import com.wordflow.android.ui.theme.RatingHard

/** Filled, color-coded rating button (Anki/AnkiDroid pattern). */
@Composable
fun RowScope.RatingButton(label: String, interval: String, color: Color, onClick: () -> Unit) {
    Button(
        onClick = onClick,
        modifier = Modifier
            .weight(1f)
            .height(Dimens.ratingButtonHeight),
        colors = ButtonDefaults.buttonColors(containerColor = color, contentColor = Color.White),
        shape = MaterialTheme.shapes.medium,
        contentPadding = PaddingValues(horizontal = 4.dp, vertical = 8.dp),
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(label, fontWeight = FontWeight.Bold, fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (interval.isNotBlank()) {
                Text(interval, fontSize = 11.sp, color = Color.White.copy(alpha = 0.85f), maxLines = 1)
            }
        }
    }
}

@Composable
fun RatingButtonRow(intervals: Map<String, String>, onRate: (Int) -> Unit, modifier: Modifier = Modifier) {
    Row(modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        RatingButton("Again", intervals["again"].orEmpty(), RatingAgain) { onRate(RATING_AGAIN) }
        RatingButton("Hard", intervals["hard"].orEmpty(), RatingHard) { onRate(RATING_HARD) }
        RatingButton("Good", intervals["good"].orEmpty(), RatingGood) { onRate(RATING_GOOD) }
        RatingButton("Easy", intervals["easy"].orEmpty(), RatingEasy) { onRate(RATING_EASY) }
    }
}
