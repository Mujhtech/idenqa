plugins {
    id("com.android.library")
}

android {
    namespace = "dev.idenqa.sdk"
    compileSdk = 37
    defaultConfig { minSdk = 26 }
    buildFeatures { buildConfig = false }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    api("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.11.0")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation("com.squareup.moshi:moshi:1.15.2")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit:2.3.21")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
}

// AGP's built-in Kotlin compiler is 2.2.0, which reads metadata up to 2.3.0.
// Force the whole Kotlin test stack to 2.3.21 so a transitive 2.4.x bump cannot
// break unit-test compilation until the Android toolchain moves to Kotlin 2.4.
configurations.configureEach {
    resolutionStrategy {
        force(
            "org.jetbrains.kotlin:kotlin-stdlib:2.3.21",
            "org.jetbrains.kotlin:kotlin-test:2.3.21",
            "org.jetbrains.kotlin:kotlin-test-junit:2.3.21",
        )
    }
}

tasks.withType<org.gradle.api.tasks.testing.Test>().configureEach {
    systemProperty("idenqa.repo.root", rootProject.projectDir.parentFile.parentFile.absolutePath)
}
