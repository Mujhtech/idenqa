package dev.idenqa.sdk

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith
import kotlin.test.assertEquals

class PoseTest {
    private fun face(yaw: Double = 0.0, closed: Double = 0.0, count: Int = 1) = CaptureFacePose(count,yaw,0.0,0.0,0.5,0.5,0.4,0.5,closed,closed)
    @Test fun directionAndContiguousHoldRequired() {
        val gate=CapturePoseGate(LivenessChallenge("right",LivenessPrompt.TURN_RIGHT,5000))
        for(time in 0..450 step 150) assertFalse(gate.update(face(),time.toDouble()).complete)
        for(time in 600..1200 step 150) assertFalse(gate.update(face(-20.0),time.toDouble()).complete)
        assertFalse(gate.update(face(20.0),1350.0).complete)
        assertFalse(gate.update(face(20.0),1500.0).complete)
        assertFalse(gate.update(face(20.0),1650.0,false).complete)
        for(time in listOf(1800.0,1950.0,2100.0)) assertFalse(gate.update(face(20.0),time).complete)
        assertTrue(gate.update(face(20.0),2250.0).complete)
    }
    @Test fun blinkRequiresTwoClosedSamplesThenOpenHold() {
        val gate=CapturePoseGate(LivenessChallenge("blink",LivenessPrompt.BLINK,5000))
        for(time in listOf(0.0,150.0,300.0,450.0)) gate.update(face(),time)
        assertFalse(gate.update(face(closed=1.0),600.0).complete)
        assertFalse(gate.update(face(),750.0).complete)
        assertFalse(gate.update(face(closed=1.0),900.0).complete)
        assertFalse(gate.update(face(closed=1.0),1000.0).complete)
        for(time in listOf(1100.0,1250.0,1400.0)) assertFalse(gate.update(face(),time).complete)
        assertTrue(gate.update(face(),1550.0).complete)
    }
    @Test fun staleInvalidAndLostFaceMeasurementsFailClosed() {
        val gate=CapturePoseGate(LivenessChallenge("neutral",LivenessPrompt.NEUTRAL,5000))
        gate.update(face(),0.0); gate.update(face(),150.0)
        assertFalse(gate.update(face(),2000.0).complete)
        assertFalse(gate.update(face(),2000.0).complete)
        assertFalse(gate.update(face(Double.NaN),2150.0).complete)
        assertFalse(gate.update(face(count=0),2300.0).complete)
        assertFalse(gate.update(face(count=2),2450.0).complete)
    }
    @Test fun posePolicyMatchesPublishedBounds() {
        assertFailsWith<IdenqaException.InvalidConfiguration> { CapturePosePolicy(targetDegrees=14.0) }
        assertFailsWith<IdenqaException.InvalidConfiguration> { CapturePosePolicy(toleranceDegrees=9.0) }
        assertFailsWith<IdenqaException.InvalidConfiguration> { CapturePosePolicy(holdDurationMilliseconds=299.0) }
        assertFailsWith<IdenqaException.InvalidConfiguration> { CapturePosePolicy(targetDegrees=20.5) }
    }
    @Test fun invalidQualityMeasurementsFailClosed() {
        val quality=CaptureQualityMeasurement(720,720,1,Double.NaN,0.5,0.5,0.0,1)
        assertEquals(listOf("invalid_measurement"),quality.failures(CaptureQualityPolicy(320,320,1024)))
    }
}
