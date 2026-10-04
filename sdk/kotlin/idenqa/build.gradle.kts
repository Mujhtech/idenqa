plugins {
    id("com.android.library")
}

android {
    namespace = "dev.idenqa.sdk"
    compileSdk = 37
    defaultConfig {
        minSdk = 26
        testInstrumentationRunner = "dev.idenqa.sdk.PoseSmoke"
    }
    buildFeatures { buildConfig = false }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation("org.jetbrains.kotlin:kotlin-stdlib:2.4.20")
    api("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.11.0")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation("com.squareup.moshi:moshi:1.15.2")
    implementation("com.google.mediapipe:tasks-vision:1.0.0")
    // MediaPipe's published POM requests older vulnerable transitive versions.
    implementation("com.google.guava:guava:33.7.2-android")
    implementation("com.google.protobuf:protobuf-javalite:4.36.2")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit:2.3.21")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
}

// Pin Kotlin test artifacts independently from the SDK's stdlib. Declare stdlib
// above so it does not override lint or compiler tool dependencies.
configurations.configureEach {
    resolutionStrategy {
        force(
            "org.jetbrains.kotlin:kotlin-test:2.4.20",
            "org.jetbrains.kotlin:kotlin-test-junit:2.3.21",
        )
    }
}

tasks.withType<org.gradle.api.tasks.testing.Test>().configureEach {
    systemProperty("idenqa.repo.root", rootProject.projectDir.parentFile.parentFile.absolutePath)
}
