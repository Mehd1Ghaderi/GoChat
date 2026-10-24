package io.github.ssenerg.gochat.core

import gochat.v1.ApiError
import gochat.v1.GetChannelRequest
import gochat.v1.GetChannelResponse
import gochat.v1.Channel
import io.github.ssenerg.gochat.core.api.ApiClient
import io.github.ssenerg.gochat.core.api.ApiException
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.content.OutgoingContent
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ApiClientTest {
    private val proto = headersOf(HttpHeaders.ContentType, "application/proto")

    @Test
    fun callsSendProtobufWithTheToken() = runTest {
        val engine = MockEngine { req ->
            assertEquals("https://chat.test/api/v1/rpc/gochat.v1.ChannelService/GetChannel", req.url.toString())
            assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
            val body = (req.body as OutgoingContent.ByteArrayContent).bytes()
            assertEquals("c1", GetChannelRequest.ADAPTER.decode(body).channel_id)
            respond(GetChannelResponse(channel = Channel(id = "c1", display_name = "Town")).encode(), HttpStatusCode.OK, proto)
        }
        val api = ApiClient("chat.test", "tok", engine)
        val res = api.call("gochat.v1.ChannelService/GetChannel", GetChannelRequest(channel_id = "c1"), GetChannelResponse.ADAPTER)
        assertEquals("Town", res.channel?.display_name)
    }

    @Test
    fun refusalsCarryTheServersReason() = runTest {
        val engine = MockEngine {
            respond(ApiError(code = 7, message = "permission denied").encode(), HttpStatusCode.Forbidden, proto)
        }
        val e = assertFailsWith<ApiException> {
            ApiClient("https://chat.test", "tok", engine).call("gochat.v1.ChannelService/GetChannel", GetChannelRequest(), GetChannelResponse.ADAPTER)
        }
        assertEquals(7, e.code)
        assertEquals("permission denied", e.message)
    }

    @Test
    fun aBareLimitStillReads() = runTest {
        val engine = MockEngine { respond("too many requests", HttpStatusCode.TooManyRequests) }
        val e = assertFailsWith<ApiException> {
            ApiClient("https://chat.test", engine = engine).call("gochat.v1.AuthService/Me", GetChannelRequest(), GetChannelResponse.ADAPTER)
        }
        assertEquals(ApiException.RESOURCE_EXHAUSTED, e.code)
        assertTrue(e.message!!.contains("Too many"))
    }

    @Test
    fun theGatewayFollowsTheScheme() {
        assertEquals("wss://chat.test/api/v1/gateway", ApiClient("https://chat.test").gatewayUrl)
        assertEquals("ws://localhost:8080/api/v1/gateway", ApiClient("http://localhost:8080/").gatewayUrl)
    }
}
