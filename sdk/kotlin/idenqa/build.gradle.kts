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
    implementation("com.squareup.okhttp3:okhttp:5.3.0")
    implementation("com.squareup.moshi:moshi:1.15.2")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit:2.4.20")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
}

tasks.withType<org.gradle.api.tasks.testing.Test>().configureEach {
    systemProperty("idenqa.repo.root", rootProject.projectDir.parentFile.parentFile.absolutePath)
}
