package dev.idenqa.sdk

import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.RectF
import android.view.View

internal class CapturePoseRing(context: Context, private val segments: Int): View(context) {
    private val paint=Paint(Paint.ANTI_ALIAS_FLAG).apply { style=Paint.Style.STROKE; strokeCap=Paint.Cap.ROUND; strokeWidth=7*resources.displayMetrics.density }
    private var index=0
    private var progress=0.0
    fun update(index: Int, progress: Double) {
        this.index=index; this.progress=progress.coerceIn(0.0,1.0)
        contentDescription="Movement ${index+1} of $segments, ${(progress*100).toInt()} percent"
        invalidate()
    }
    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val inset=paint.strokeWidth
        val size=minOf(width,height).toFloat()-inset*2
        val left=(width-size)/2; val top=(height-size)/2
        val rect=RectF(left,top,left+size,top+size)
        repeat(segments) { part ->
            val start=-90f+part*360f/segments+3
            val sweep=360f/segments-6
            paint.color=Color.argb(80,255,255,255); canvas.drawArc(rect,start,sweep,false,paint)
            val fraction=if(part<index) 1.0 else if(part==index) progress else 0.0
            if(fraction>0) { paint.color=Color.rgb(81,220,140); canvas.drawArc(rect,start,(sweep*fraction).toFloat(),false,paint) }
        }
    }
}

internal fun nativePoseInstruction(prompt: LivenessPrompt, feedback: String): String = when(feedback) {
    "find_face" -> "Keep your face in view"
    "one_face" -> "Only one person should be in view"
    "move_closer" -> "Move a little closer"
    "move_back" -> "Move a little further away"
    "center_face" -> "Centre your face in the guide"
    "face_forward" -> "Look straight at the camera"
    "hold_still" -> "Hold still"
    "open_eyes" -> "Open your eyes"
    "quality" -> "Use even lighting and hold the camera steady"
    else -> when(prompt) {
        LivenessPrompt.NEUTRAL -> "Look straight at the camera"
        LivenessPrompt.TURN_LEFT -> "Slowly turn your head left"
        LivenessPrompt.TURN_RIGHT -> "Slowly turn your head right"
        LivenessPrompt.LOOK_UP -> "Slowly look up"
        LivenessPrompt.LOOK_DOWN -> "Slowly look down"
        LivenessPrompt.BLINK -> "Blink, then open your eyes"
    }
}
