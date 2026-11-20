import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "NetDream",
  identifier: "com.example.netdream",
  version: "0.4.0",
  // 应用图标（窗口标题栏/任务栏/打包产物）：正方形 PNG，放 resources/icon.png。
  icon: "resources/icon.png",
  // The Go package of the app, a directory of this project (backend/).
  main: "./backend",
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run --cwd frontend dev",
  buildCommand: "bun run --cwd frontend build",
  frontendDist: "frontend/dist",
  bindings: "frontend/src/mygo.ts",
  out: "build",
  // Phones: `mygo dev -platform ios` and `-platform android` run the app on a
  // simulator, an emulator or a device (`mygo devices` lists them, and
  // `mygo doctor` tells what is missing). What devices and stores need:
  // ios: { team: "ABCDE12345", infoPlist: { NSCameraUsageDescription: "Scan receipts." } },
  // android: { keystore: "upload.jks", keyAlias: "upload", permissions: ["CAMERA"] },
});
