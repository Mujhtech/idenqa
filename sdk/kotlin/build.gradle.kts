buildscript {
    dependencies {
        // Use the repository-selected compiler with AGP's built-in Kotlin support.
        classpath("org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.21")

        // Patch AGP's existing tooling graph without changing SDK runtime resolution.
        constraints {
            add("classpath", "com.google.guava:guava:33.8.0-jre") {
                because("Fix unbounded deserialization allocation (GHSA-xxph-c9ww-hj94)")
            }
            add("classpath", "org.apache.commons:commons-lang3:3.22.0") {
                because("Fix recursive class-name parsing (GHSA-j288-q9x7-2f5v)")
            }
            add("classpath", "org.bitbucket.b_c:jose4j:0.9.7") {
                because("Bound compressed JWE content (GHSA-3677-xxcr-wjqv)")
            }
            add("classpath", "org.bouncycastle:bcpkix-jdk18on:1.87") {
                because("Fix composite signature verification (GHSA-wg6q-6289-32hp)")
            }
            add("classpath", "org.bouncycastle:bcprov-jdk18on:1.87") {
                because("Fix certificate name constraints and ASN.1 nesting guards")
            }
            add("classpath", "org.bouncycastle:bcutil-jdk18on:1.87") {
                because("Keep the existing Bouncy Castle tooling modules aligned")
            }
            add("classpath", "org.jdom:jdom2:2.0.6.1") {
                because("Fix XML external entity handling (GHSA-2363-cqg2-863c)")
            }
        }
    }
}

plugins {
    id("com.android.library") version "9.5.1" apply false
}
