buildscript {
    dependencies {
        // Use the repository-selected compiler with AGP's built-in Kotlin support.
        classpath("org.jetbrains.kotlin:kotlin-gradle-plugin:2.3.21")
    }
}

plugins {
    id("com.android.library") version "9.4.1" apply false
}
