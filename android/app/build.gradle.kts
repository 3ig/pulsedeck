plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "app.pulsedeck.client"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.pulsedeck.client"
        minSdk = 26
        targetSdk = 35
        // Release builds pass -PappVersion=1.2.3 (and -PappVersionCode=N).
        versionCode = (project.findProperty("appVersionCode") as String?)?.toIntOrNull() ?: 1
        versionName = (project.findProperty("appVersion") as String?) ?: "dev"
    }

    // The dashboard UI is the same web client the server embeds; it is
    // packaged straight from the repository's client/ directory.
    sourceSets["main"].assets.srcDir("../../client")

    // A release keystore is optional: without one (PULSEDECK_KEYSTORE unset) the
    // release build falls back to the debug key, which installs fine but cannot
    // be updated in place by a build signed on another machine.
    val keystore = System.getenv("PULSEDECK_KEYSTORE")
    signingConfigs {
        if (keystore != null) {
            create("release") {
                storeFile = file(keystore)
                storePassword = System.getenv("PULSEDECK_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("PULSEDECK_KEY_ALIAS") ?: "pulsedeck"
                keyPassword = System.getenv("PULSEDECK_KEY_PASSWORD") ?: System.getenv("PULSEDECK_KEYSTORE_PASSWORD")
            }
        }
    }
    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}
