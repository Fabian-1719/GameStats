package gamestats

import cask.*
import ujson.*
import scala.util.Try

object Main extends cask.MainRoutes:
  override def port: Int = 8081
  override def host: String = "0.0.0.0"

  override def main(args: Array[String]): Unit =
    super.main(args)
    new java.util.concurrent.CountDownLatch(1).await()

  private val service = new AnalysisService()

  @cask.post("/v1/analyze")
  def analyze(request: cask.Request): cask.Response[String] =
    Try(JsonDecoding.batch(ujson.read(request.text()))).toEither match
      case Left(error) => jsonResponse(400, Obj("error" -> Str(Option(error.getMessage).getOrElse("invalid batch"))))
      case Right(batch) =>
        service.analyze(batch) match
          case Some(result) => jsonResponse(200, result.toJson)
          case None => jsonResponse(422, Obj("error" -> Str(s"unknown gameId: ${batch.gameId}")))

  @cask.get("/v1/matches/:id")
  def getMatch(id: String, gameId: Option[String] = None): cask.Response[String] =
    val matches = service.getMatches(id, gameId)
    matches match
      case Nil => jsonResponse(404, Obj("error" -> Str("match not found")))
      case single :: Nil => jsonResponse(200, single.toJson)
      case several => jsonResponse(200, Arr.from(several.map(_.toJson)))

  @cask.get("/v1/patterns")
  def patterns(
      gameId: Option[String] = None,
      `type`: Option[String] = None,
      matchId: Option[String] = None
  ): cask.Response[String] =
    jsonResponse(200, Arr.from(service.getPatterns(gameId, `type`, matchId).map(_.toJson)))

  @cask.get("/v1/stats/players/:id")
  def playerStats(id: String): cask.Response[String] =
    service.getPlayerStats(id) match
      case Some(stats) => jsonResponse(200, stats)
      case None => jsonResponse(404, Obj("error" -> Str("player not found")))

  @cask.get("/v1/stats/general")
  def generalStats(): cask.Response[String] = jsonResponse(200, service.generalStats())

  @cask.get("/v1/games/:gameId/stats")
  def gameStats(gameId: String): cask.Response[String] =
    service.gameStats(gameId) match
      case Some(stats) => jsonResponse(200, stats)
      case None => jsonResponse(404, Obj("error" -> Str(s"unknown gameId: $gameId")))

  @cask.get("/v1/games/:gameId/leaderboard")
  def leaderboard(gameId: String): cask.Response[String] =
    jsonResponse(200, Arr.from(service.leaderboard(gameId)))

  @cask.get("/health")
  def health(): cask.Response[String] = jsonResponse(200, Obj("status" -> Str("ok")))

  private def jsonResponse(status: Int, body: Value): cask.Response[String] =
    cask.Response(ujson.write(body), statusCode = status, headers = Seq("Content-Type" -> "application/json"))

  initialize()