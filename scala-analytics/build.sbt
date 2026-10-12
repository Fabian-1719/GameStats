ThisBuild / scalaVersion := "3.3.6"
ThisBuild / organization := "gamestats"
ThisBuild / version := "0.1.0"

lazy val root = (project in file("."))
  .settings(
    name := "scala-analytics",
    assembly / mainClass := Some("gamestats.Main"),
    assembly / assemblyJarName := "scala-analytics.jar",
    libraryDependencies ++= Seq(
      "com.lihaoyi" %% "cask" % "0.10.2",
      "com.lihaoyi" %% "upickle" % "4.0.2",
      "org.scalameta" %% "munit" % "1.0.4" % Test
    )
  )