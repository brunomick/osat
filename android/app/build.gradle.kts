import org.apache.tools.ant.taskdefs.condition.Os

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "ar.osat"
    compileSdk = 36
    ndkVersion = "27.0.12077973"

    defaultConfig {
        applicationId = "ar.osat"
        minSdk = 26
        targetSdk = 35
        versionCode = 7
        versionName = "1.1.0-beta.1"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("go/jniLibs"))
    packaging {
        jniLibs.useLegacyPackaging = true // el backend se ejecuta: tiene que quedar extraído
    }
}

// El backend es el mismo osat de escritorio compilado para Android (main_android.go). Se arma como
// libosat.so para que Android lo instale en nativeLibraryDir, la única carpeta desde la que una app
// puede ejecutar un binario propio.
val repo = rootDir.parentFile
val win = Os.isFamily(Os.FAMILY_WINDOWS)

val buildFrontend by tasks.registering(Exec::class) {
    workingDir = File(repo, "frontend")
    commandLine(if (win) "npm.cmd" else "npm", "run", "build")
}

val abis = mapOf("arm64-v8a" to Pair("arm64", "aarch64"), "x86_64" to Pair("amd64", "x86_64"))
val goTasks = abis.map { (abi, arch) ->
    tasks.register("buildGo-$abi", Exec::class) {
        dependsOn(buildFrontend)
        val host = if (win) "windows-x86_64" else if (Os.isFamily(Os.FAMILY_MAC)) "darwin-x86_64" else "linux-x86_64"
        val cc = File(android.ndkDirectory, "toolchains/llvm/prebuilt/$host/bin/${arch.second}-linux-android26-clang" + if (win) ".cmd" else "")
        val out = layout.buildDirectory.file("go/jniLibs/$abi/libosat.so").get().asFile
        workingDir = repo
        environment("CGO_ENABLED", "1")
        environment("GOOS", "android")
        environment("GOARCH", arch.first)
        environment("CC", cc.path)
        commandLine("go", "build", "-buildmode=pie", "-trimpath", "-ldflags=-s -w", "-o", out.path, ".")
    }
}

tasks.named("preBuild") { dependsOn(goTasks) }
