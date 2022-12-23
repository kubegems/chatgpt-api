import dotenv from 'dotenv-safe'
import express from 'express'
import { hostname } from 'os'

import { ChatGPTAPIBrowser } from '../src'

dotenv.config()

async function getapi() {
  const host = hostname()
  const seps = host.split('-')
  const idx = seps[seps.length - 1]
  const email =
    process.env['OPENAI_EMAIL'] || process.env[`OPENAI_EMAIL_${idx}`]
  const password =
    process.env['OPENAI_PASSWORD'] || process.env[`OPENAI_PASSWORD_${idx}`]

  console.log(`account ${idx} ${email} used`)
  const api = new ChatGPTAPIBrowser({
    email,
    password,
    debug: false,
    minimize: true
  })
  await api.initSession()
  return api
}

async function server() {
  const api = await getapi()

  const app = express()
  const port = 3000
  app.get('/', async (req, res) => {
    const q = req.query.q
    const start = Date.now()
    const conversationId = req.query.conversationId
    const parentMessageId = req.query.parentMessageId
    const messageId = req.query.messageId
    console.log(q, conversationId, parentMessageId, messageId)
    let result = {
      conversationId,
      response: '',
      messageId: ''
    }
    try {
      result = await api.sendMessage(q, {
        conversationId,
        parentMessageId,
        messageId
      })
    } catch (error) {
      console.table({
        error
      })
      res.set('instance', hostname())
      res.send({ error })
      return
    }
    const millis = Date.now() - start
    console.table({
      timeused: Math.floor(millis / 1000),
      instance: hostname(),
      ...req.query
    })
    if (result != undefined) {
      res.set('instance', hostname())
      res.set(
        'conversationId',
        req.query.conversationId || result.conversationId
      )
    }
    console.table({ instance: hostname(), ...result })
    res.send({ instance: hostname(), ...result })
  })

  app.get('/ready', async (req, res) => {
    res.send(`ok, ${hostname()}`)
  })

  app.listen(port, () => {
    console.log(`Example app listening on port ${port}`)
  })
}

server()
