// SPDX-License-Identifier: AGPL-3.0-or-later

package site

// feedXSL turns Atom and RSS feeds into a readable page when someone opens
// a feed in a browser, with a note on what a feed is.
const feedXSL = `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:atom="http://www.w3.org/2005/Atom">
<xsl:output method="html" encoding="utf-8" indent="yes"/>
<xsl:template match="/">
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title><xsl:value-of select="atom:feed/atom:title|rss/channel/title"/> (feed)</title>
<style>
body{margin:0;background:#0f1420;color:#ece6d9;font:18px/1.6 Georgia,serif}
main{max-width:46rem;margin:0 auto;padding:3rem 1rem}
a{color:#7fb0ff}
.note{font:15px/1.5 system-ui,sans-serif;color:#9aa3b5;border-left:2px solid #7fb0ff;padding:.5rem 1rem;background:#151b29}
h1{font-size:2.25rem;line-height:1.1;margin:2rem 0 .5rem}
ul{list-style:none;padding:0;border-top:1px solid #2a3345}
li{padding:1rem 0;border-bottom:1px solid #2a3345}
li a{font-weight:600;font-size:1.2rem;color:#ece6d9;text-decoration:none}
li a:hover{text-decoration:underline}
time{display:block;font:14px system-ui,sans-serif;color:#9aa3b5}
@media (prefers-color-scheme:light){body{background:#f6f3ec;color:#1c1b18}.note{background:#efebe2;color:#5c584f}li a{color:#1c1b18}time{color:#5c584f}ul,li{border-color:#d8d2c4}a{color:#2f5aa0}}
</style>
</head>
<body><main>
<p class="note">This is a web feed. Copy this page's address into a feed reader to get new posts as they are published.</p>
<xsl:apply-templates select="atom:feed|rss/channel"/>
</main></body>
</html>
</xsl:template>
<xsl:template match="atom:feed">
<h1><xsl:value-of select="atom:title"/></h1>
<p><a href="{atom:link[@rel='alternate']/@href}">Visit the site</a></p>
<ul><xsl:for-each select="atom:entry"><li>
<a href="{atom:link[@rel='alternate']/@href}"><xsl:value-of select="atom:title"/></a>
<time><xsl:value-of select="substring(atom:published,1,10)"/></time>
</li></xsl:for-each></ul>
</xsl:template>
<xsl:template match="channel">
<h1><xsl:value-of select="title"/></h1>
<p><a href="{link}">Visit the site</a></p>
<ul><xsl:for-each select="item"><li>
<a href="{link}"><xsl:value-of select="title"/></a>
<time><xsl:value-of select="pubDate"/></time>
</li></xsl:for-each></ul>
</xsl:template>
</xsl:stylesheet>
`
